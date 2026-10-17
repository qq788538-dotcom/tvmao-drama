// apps/api/internal/agent/workflow_test.go
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// recordingTool 是 workflow 测试用的可配置 Tool。它记录被调用时的 JSON args，
// 并发出一个 image_info artifact，其 URL 在构造时固定，这样下游 step 能从它
// 取 `{{step.<name>.url}}`。
type recordingTool struct {
	name     string
	emitURL  string
	lastArgs string
	err      error
}

func (t *recordingTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: t.name}, nil
}

func (t *recordingTool) InvokableRun(ctx context.Context, args string, _ ...einotool.Option) (string, error) {
	t.lastArgs = args
	if t.err != nil {
		return "", t.err
	}
	EmitFromContext(ctx)(Artifact{
		ArtifactType: "image_info",
		ArtifactID:   "art_" + t.name,
		URL:          t.emitURL,
		Name:         t.name,
	})
	return "done_" + t.name, nil
}

// TestExecuteWorkflow_Sequential 覆盖最核心的行为：step 2 能通过
// `{{step.<step1>.url}}` 引用 step 1 发出的 URL。它还钉死了帧顺序，让 WS client
// 对每个 step 依次拿到 step_start → tool_event → step_end 这些 block。
func TestExecuteWorkflow_Sequential(t *testing.T) {
	toolA := &recordingTool{name: "tool_a", emitURL: "http://x/a.png"}
	toolB := &recordingTool{name: "tool_b", emitURL: "http://x/b.png"}
	reg := newStubRegistryWith(toolA, toolB)

	steps := []WorkflowStep{
		{Name: "first", ToolName: "tool_a", Inputs: map[string]any{"prompt": "{{input.prompt}}"}},
		{Name: "second", ToolName: "tool_b", Inputs: map[string]any{"ref": "{{step.first.url}}"}},
	}
	sink := &stubFrameSink{}

	executed, err := ExecuteWorkflow(
		context.Background(),
		reg,
		steps,
		map[string]any{"prompt": "draw shiba"},
		"t1", "a1", sink,
	)
	if err != nil {
		t.Fatalf("ExecuteWorkflow: %v", err)
	}
	if executed != 2 {
		t.Fatalf("expected 2 executed steps, got %d", executed)
	}

	if !strings.Contains(toolA.lastArgs, "draw shiba") {
		t.Fatalf("tool_a should have received the resolved prompt, got %q", toolA.lastArgs)
	}
	if !strings.Contains(toolB.lastArgs, "http://x/a.png") {
		t.Fatalf("tool_b should have received tool_a's URL via {{step.first.url}}, got %q", toolB.lastArgs)
	}

	wantTypes := []FrameType{
		FrameStepStart,
		FrameContentBlockStart,
		FrameStepEnd,
		FrameStepStart,
		FrameContentBlockStart,
		FrameStepEnd,
	}
	got := sink.Frames()
	if len(got) != len(wantTypes) {
		t.Fatalf("expected %d frames, got %d: %v", len(wantTypes), len(got), frameTypes(got))
	}
	for i, want := range wantTypes {
		if got[i].Type != want {
			t.Fatalf("frame[%d] type = %s, want %s; full: %v", i, got[i].Type, want, frameTypes(got))
		}
	}

	// 健全性检查：每个 tool_event 帧都必须携带工具名 + 一个 artifact。
	for _, f := range got {
		if f.Block != nil && f.Block.Type == "tool_event" {
			if f.Block.Name == "" {
				t.Fatalf("tool_event frame missing block.Name: %+v", f)
			}
			if len(f.Block.Artifacts) != 1 {
				t.Fatalf("tool_event frame should carry 1 artifact, got %d", len(f.Block.Artifacts))
			}
		}
	}

	// step_start block 应在 Block.Text 里带上 step 名字，这样 UI 不用再多一次往返
	// 就能渲染进度指示器的标签。
	stepStartTexts := []string{}
	for _, f := range got {
		if f.Type == FrameStepStart && f.Block != nil {
			stepStartTexts = append(stepStartTexts, f.Block.Text)
		}
	}
	if len(stepStartTexts) != 2 || stepStartTexts[0] != "first" || stepStartTexts[1] != "second" {
		t.Fatalf("step_start texts should be [first second], got %v", stepStartTexts)
	}
}

// TestExecuteWorkflow_InputSubstitution 验证 `{{input.<key>}}` 模板即使在没有任何
// 前序 step 产出该值时（单 step 的 workflow），也能从 initialVars 填上。
func TestExecuteWorkflow_InputSubstitution(t *testing.T) {
	tool := &recordingTool{name: "echo", emitURL: "http://x/echo.png"}
	reg := newStubRegistryWith(tool)

	steps := []WorkflowStep{
		{Name: "only", ToolName: "echo", Inputs: map[string]any{
			"prompt": "{{input.prompt}}",
			"style":  "{{input.style}}",
			"size":   1024, // 非字符串原样透传
		}},
	}
	sink := &stubFrameSink{}

	_, err := ExecuteWorkflow(
		context.Background(),
		reg,
		steps,
		map[string]any{"prompt": "雪地柴犬", "style": "ghibli"},
		"t1", "a1", sink,
	)
	if err != nil {
		t.Fatalf("ExecuteWorkflow: %v", err)
	}
	if !strings.Contains(tool.lastArgs, "雪地柴犬") {
		t.Fatalf("expected resolved prompt in args, got %q", tool.lastArgs)
	}
	if !strings.Contains(tool.lastArgs, "ghibli") {
		t.Fatalf("expected resolved style in args, got %q", tool.lastArgs)
	}
	if !strings.Contains(tool.lastArgs, "1024") {
		t.Fatalf("non-string input should pass through, got %q", tool.lastArgs)
	}
}

// TestExecuteWorkflow_MissingTool 验证引用一个不在 registry 里的工具时，会暴露一个
// error，并且最后还有一个 stop_reason=error 的 step_end 帧，这样 WS client 能渲染
// 出失败的 step 状态。
func TestExecuteWorkflow_MissingTool(t *testing.T) {
	reg := newStubRegistryWith() // 空 registry
	steps := []WorkflowStep{
		{Name: "bad", ToolName: "does_not_exist", Inputs: map[string]any{}},
	}
	sink := &stubFrameSink{}

	executed, err := ExecuteWorkflow(context.Background(), reg, steps, nil, "t1", "a1", sink)
	if err == nil {
		t.Fatal("expected error for missing tool, got nil")
	}
	if executed != 0 {
		t.Fatalf("no step should count as executed, got %d", executed)
	}
	if !strings.Contains(err.Error(), "does_not_exist") {
		t.Fatalf("error should name the missing tool, got %v", err)
	}

	got := sink.Frames()
	if len(got) < 2 {
		t.Fatalf("expected at least step_start + step_end frames, got %d", len(got))
	}
	last := got[len(got)-1]
	if last.Type != FrameStepEnd || last.StopReason != "error" {
		t.Fatalf("expected final frame step_end(error), got %+v", last)
	}
}

// TestExecuteWorkflow_EmitsStepFrames 覆盖一次 2-step 运行的严格帧顺序。它还断言：
// 如果第一个 step 的工具失败，第二个 step 绝不会执行，并且我们对失败的那个 step
// 恰好观察到 step_start → step_end。
func TestExecuteWorkflow_EmitsStepFrames(t *testing.T) {
	t.Run("happy path: each step emits start+event+end", func(t *testing.T) {
		toolA := &recordingTool{name: "tool_a", emitURL: "http://x/a.png"}
		toolB := &recordingTool{name: "tool_b", emitURL: "http://x/b.png"}
		reg := newStubRegistryWith(toolA, toolB)
		steps := []WorkflowStep{
			{Name: "s1", ToolName: "tool_a", Inputs: map[string]any{}},
			{Name: "s2", ToolName: "tool_b", Inputs: map[string]any{}},
		}
		sink := &stubFrameSink{}
		if _, err := ExecuteWorkflow(context.Background(), reg, steps, nil, "t1", "a1", sink); err != nil {
			t.Fatalf("ExecuteWorkflow: %v", err)
		}

		got := sink.Frames()
		stepStarts, stepEnds, toolEvents := 0, 0, 0
		for _, f := range got {
			switch {
			case f.Type == FrameStepStart:
				stepStarts++
			case f.Type == FrameStepEnd:
				stepEnds++
				if f.StopReason != "ok" {
					t.Fatalf("happy-path step_end should have stop_reason=ok, got %q", f.StopReason)
				}
			case f.Block != nil && f.Block.Type == "tool_event":
				toolEvents++
			}
		}
		if stepStarts != 2 || stepEnds != 2 || toolEvents != 2 {
			t.Fatalf("expected 2/2/2 of step_start/step_end/tool_event, got %d/%d/%d", stepStarts, stepEnds, toolEvents)
		}
	})

	t.Run("step 1 fails -> step 2 never runs", func(t *testing.T) {
		toolA := &recordingTool{name: "tool_a", err: errors.New("boom")}
		toolB := &recordingTool{name: "tool_b", emitURL: "http://x/b.png"}
		reg := newStubRegistryWith(toolA, toolB)
		steps := []WorkflowStep{
			{Name: "s1", ToolName: "tool_a", Inputs: map[string]any{}},
			{Name: "s2", ToolName: "tool_b", Inputs: map[string]any{}},
		}
		sink := &stubFrameSink{}
		executed, err := ExecuteWorkflow(context.Background(), reg, steps, nil, "t1", "a1", sink)
		if err == nil {
			t.Fatal("expected error when step 1 tool fails, got nil")
		}
		if executed != 0 {
			t.Fatalf("failed first step should count 0 executed, got %d", executed)
		}
		if toolB.lastArgs != "" {
			t.Fatalf("step 2 should not run after step 1 failure, got args %q", toolB.lastArgs)
		}
		got := sink.Frames()
		last := got[len(got)-1]
		if last.Type != FrameStepEnd || last.StopReason != "error" {
			t.Fatalf("expected final step_end(error), got %+v", last)
		}
		// 应该只发出了一个 step_start（s1）。
		stepStarts := 0
		for _, f := range got {
			if f.Type == FrameStepStart {
				stepStarts++
			}
		}
		if stepStarts != 1 {
			t.Fatalf("expected exactly 1 step_start before failure, got %d", stepStarts)
		}
	})
}
