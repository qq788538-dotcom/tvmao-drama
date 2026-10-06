package skillruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// TestNewRuntimeRequiresShellFactory —— 显式传 nil shell 工厂时 Run 必须报错，
// 保证生产绝不会因为漏配沙箱而静默退回不受限执行。
func TestNewRuntimeRequiresShellFactory(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "story-style", "SKILL.md"), `---
name: story-style
description: Story style
---
Return the draft.
`)
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Snapshot(context.Background(), "story-style")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(catalog, workspace, func(context.Context, ModelConfig) (model.ToolCallingChatModel, error) {
		return &scriptedModel{state: &scriptedState{}}, nil
	}, 10, nil)
	if _, err := runtime.Run(context.Background(), RunRequest{
		Skill: snapshot, Model: ModelConfig{APIKey: "sk", Model: "test"}, Input: "go",
	}); err == nil {
		t.Fatal("expected Run to fail when shell factory is nil")
	}
}

// TestRuntimeUsesInjectedShellFactory —— Run 的 execute 路径必须经注入的工厂造 shell，
// root 与 fork 共用同一实例。断言工厂被调用且 execute 脚本确实跑通。
func TestRuntimeUsesInjectedShellFactory(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "script-style", "SKILL.md"), `---
name: script-style
description: Script style
---
Run scripts/generate.py and use its result.
`)
	writeTestFile(t, filepath.Join(root, "script-style", "scripts", "generate.py"), `from pathlib import Path
print(Path("candidate.json").read_text())
`)
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Snapshot(context.Background(), "script-style")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &shellModelState{}
	var factoryCalls int32
	factory := func(root, _ string) (filesystem.Shell, error) {
		atomic.AddInt32(&factoryCalls, 1)
		return NewTrustedShell(root)
	}
	runtime := NewRuntime(catalog, workspace, func(context.Context, ModelConfig) (model.ToolCallingChatModel, error) {
		return &shellScriptModel{state: state}, nil
	}, 10, factory)
	if _, err := runtime.Run(context.Background(), RunRequest{
		Skill: snapshot, Model: ModelConfig{APIKey: "sk", Model: "test"}, Input: "run it",
	}); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&factoryCalls) == 0 {
		t.Fatal("injected shell factory was never used")
	}
	if !state.sawExecuteTool || !state.sawScriptOutput {
		t.Fatalf("execute path not exercised through injected factory: %#v", state)
	}
}

func TestModelRequestTimeoutDefaultsToTenMinutes(t *testing.T) {
	if got := modelRequestTimeout(0); got != 10*time.Minute {
		t.Fatalf("default timeout = %s, want 10m", got)
	}
}

func TestModelRequestTimeoutPreservesConfiguredValue(t *testing.T) {
	configured := 17 * time.Second
	if got := modelRequestTimeout(configured); got != configured {
		t.Fatalf("configured timeout = %s, want %s", got, configured)
	}
}

func TestRuntimeLoadsSelectedSkillThenLetsModelChooseIterations(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "story-style", "SKILL.md"), `---
name: story-style
description: Story style
---
Rule: repair the draft before returning it. Style=__{{STYLE}}__.
`)
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Snapshot(context.Background(), "story-style")
	if err != nil {
		t.Fatal(err)
	}
	state := &scriptedState{}
	fake := &scriptedModel{state: state}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(catalog, workspace, func(context.Context, ModelConfig) (model.ToolCallingChatModel, error) {
		return fake, nil
	}, 12, TrustedShellFactory)

	result, err := runtime.Run(context.Background(), RunRequest{
		Skill:     loaded,
		Model:     ModelConfig{BaseURL: "https://example.test/v1", APIKey: "sk", Model: "test"},
		Input:     "create a storyboard",
		Variables: map[string]string{"STYLE": "warm"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "final storyboard" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if state.calls != 2 {
		t.Fatalf("expected skill activation followed by final response, got %d calls", state.calls)
	}
	if !state.sawForcedSkill {
		t.Fatal("first call did not carry explicit tool_choice auto (ToolChoiceAllowed, no allowlist)")
	}
	if !state.sawSkillContent {
		t.Fatal("subsequent call did not receive substituted SKILL.md content")
	}
	if !state.sawWorkspaceRules {
		t.Fatal("subsequent call did not receive relative workspace path rules")
	}
	if !state.sawChineseSystemPrompt {
		t.Fatal("single-skill runtime did not use Chinese system prompts")
	}
	if state.rootToolNames[submitArtifactToolName] {
		t.Fatalf("legacy skill unexpectedly exposed %s: %#v", submitArtifactToolName, state.rootToolNames)
	}
	// 提取链路是 inline skill,deep 预装的 todos/task 子 agent 工具是纯开销:
	// 每轮请求多带工具定义,模型还会花轮次写 todos——烧的就是 MaxIteration 预算。
	if state.rootToolNames["write_todos"] || state.rootToolNames["task"] {
		t.Fatalf("deep 内置 write_todos/task 应已禁用: %#v", state.rootToolNames)
	}
	if len(state.rootSystemInstruction) < len(genericAgentInstruction) ||
		state.rootSystemInstruction[:len(genericAgentInstruction)] != genericAgentInstruction ||
		strings.Contains(state.rootSystemInstruction, "本 Skill 声明的最终产物必须写入") {
		t.Fatalf("legacy generic instruction changed:\n%s", state.rootSystemInstruction)
	}
}

func TestRuntimeReturnsExplicitlySubmittedArtifact(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "story-style", "SKILL.md"), `---
name: story-style
description: Story style
output_artifact: candidate.json
---
Write and verify the storyboard artifact before submitting it.
`)
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Snapshot(context.Background(), "story-style")
	if err != nil {
		t.Fatal(err)
	}
	const artifact = "{\n  \"storyboard\": [1, 2]\n}\n"
	state := &scriptedState{scenario: scriptedArtifactSuccess, artifactContent: artifact}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(catalog, workspace, func(context.Context, ModelConfig) (model.ToolCallingChatModel, error) {
		return &scriptedModel{state: state}, nil
	}, 12, TrustedShellFactory)

	result, err := runtime.Run(context.Background(), RunRequest{
		Skill: loaded,
		Model: ModelConfig{BaseURL: "https://example.test/v1", APIKey: "sk", Model: "test"},
		Input: "create a storyboard",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != artifact {
		t.Fatalf("artifact content changed: got %q, want %q", result.Content, artifact)
	}
	if !state.rootToolNames[submitArtifactToolName] {
		t.Fatalf("artifact skill did not expose %s: %#v", submitArtifactToolName, state.rootToolNames)
	}
	if !strings.Contains(state.rootSystemInstruction, "candidate.json") ||
		!strings.Contains(state.rootSystemInstruction, "验证") ||
		!strings.Contains(state.rootSystemInstruction, "最后调用") ||
		!strings.Contains(state.rootSystemInstruction, submitArtifactToolName) {
		t.Fatalf("artifact instruction did not require verified submission at candidate.json:\n%s", state.rootSystemInstruction)
	}
}

func TestRuntimeArtifactSkillRequiresExplicitSubmission(t *testing.T) {
	runtime, loaded, state := newScriptedArtifactRuntime(t, scriptedArtifactNotSubmitted)

	result, err := runtime.Run(context.Background(), RunRequest{
		Skill: loaded,
		Model: ModelConfig{BaseURL: "https://example.test/v1", APIKey: "sk", Model: "test"},
		Input: "create a storyboard",
	})
	if !errors.Is(err, ErrArtifactNotSubmitted) || !strings.Contains(err.Error(), "artifact was not submitted") {
		t.Fatalf("expected ErrArtifactNotSubmitted, got result=%#v err=%v", result, err)
	}
	if result.Content != "" {
		t.Fatalf("artifact skill fell back to assistant text: %#v", result)
	}
	if !state.rootToolNames[submitArtifactToolName] {
		t.Fatalf("artifact skill did not expose %s: %#v", submitArtifactToolName, state.rootToolNames)
	}
}

func TestRuntimeArtifactSubmissionFailureDoesNotFallBackToAssistantText(t *testing.T) {
	runtime, loaded, _ := newScriptedArtifactRuntime(t, scriptedArtifactSubmitFailure)

	result, err := runtime.Run(context.Background(), RunRequest{
		Skill: loaded,
		Model: ModelConfig{BaseURL: "https://example.test/v1", APIKey: "sk", Model: "test"},
		Input: "create a storyboard",
	})
	if !errors.Is(err, ErrArtifactNotSubmitted) ||
		!strings.Contains(err.Error(), "artifact was not submitted") ||
		!strings.Contains(err.Error(), "inspect artifact") {
		t.Fatalf("expected submission failure, got result=%#v err=%v", result, err)
	}
	if result.Content != "" {
		t.Fatalf("failed submission fell back to assistant text: %#v", result)
	}
}

// TestRuntimeFeedsToolErrorBackToModel 复现生产事故：模型偶发调 ls("/")（绝对路径越界），
// Eino 默认把工具错误当致命 NodeRunError，20+ 分钟的 Run 整场报废重跑
// （2026-07-21/22 生产日志 60 次 path escapes skill root）。期望：错误文本作为
// 普通 tool 结果回给模型，模型自纠后 Run 正常完成。
func TestRuntimeFeedsToolErrorBackToModel(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "story-style", "SKILL.md"), `---
name: story-style
description: Story style
---
Rule: repair the draft before returning it.
`)
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Snapshot(context.Background(), "story-style")
	if err != nil {
		t.Fatal(err)
	}
	state := &scriptedState{scenario: scriptedLsPathEscape}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(catalog, workspace, func(context.Context, ModelConfig) (model.ToolCallingChatModel, error) {
		return &scriptedModel{state: state}, nil
	}, 12, TrustedShellFactory)

	result, err := runtime.Run(context.Background(), RunRequest{
		Skill: loaded,
		Model: ModelConfig{BaseURL: "https://example.test/v1", APIKey: "sk", Model: "test"},
		Input: "create a storyboard",
	})
	if err != nil {
		t.Fatalf("tool error killed the whole run: %v", err)
	}
	if result.Content != "final storyboard" {
		t.Fatalf("model did not recover after tool error feedback: %#v", result)
	}
	if !strings.Contains(state.lsErrorFeedback, "path escapes skill root") {
		t.Fatalf("ls error was not fed back to the model as a tool result: %q", state.lsErrorFeedback)
	}
}

func newScriptedArtifactRuntime(t *testing.T, scenario scriptedScenario) (*Runtime, Snapshot, *scriptedState) {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "story-style", "SKILL.md"), `---
name: story-style
description: Story style
output_artifact: candidate.json
---
Write and verify the storyboard artifact before submitting it.
`)
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Snapshot(context.Background(), "story-style")
	if err != nil {
		t.Fatal(err)
	}
	state := &scriptedState{scenario: scenario}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(catalog, workspace, func(context.Context, ModelConfig) (model.ToolCallingChatModel, error) {
		return &scriptedModel{state: state}, nil
	}, 12, TrustedShellFactory)
	return runtime, loaded, state
}

// trace 默认关闭：不设 .env 开关时既不给模型层传 TracePath，也不落 model-trace.jsonl。
func TestRuntimeDoesNotWriteModelTraceByDefault(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "story-style", "SKILL.md"), `---
name: story-style
description: Story style
---
Return the final storyboard.
`)
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Snapshot(context.Background(), "story-style")
	if err != nil {
		t.Fatal(err)
	}
	state := &scriptedState{}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var capturedTracePath string
	runtime := NewRuntime(catalog, workspace, func(_ context.Context, cfg ModelConfig) (model.ToolCallingChatModel, error) {
		capturedTracePath = cfg.TracePath
		return &scriptedModel{state: state}, nil
	}, 12, TrustedShellFactory)

	if _, err := runtime.Run(context.Background(), RunRequest{
		Skill: loaded,
		Model: ModelConfig{BaseURL: "https://example.test/v1", APIKey: "sk", Model: "test"},
		Input: "create a storyboard",
	}); err != nil {
		t.Fatal(err)
	}
	if capturedTracePath != "" {
		t.Fatalf("默认应不传 TracePath, got %q", capturedTracePath)
	}
	matches, err := filepath.Glob(filepath.Join(workspace.Root(), "*", "run-*", "model-trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("默认应不生成 model-trace.jsonl, got %#v", matches)
	}
}

func TestRuntimeWritesRedactedFullModelTracePerRequestWhenEnabled(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		message := map[string]any{"role": "assistant", "content": "final storyboard"}
		finishReason := "stop"
		if calls == 1 {
			message = map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []map[string]any{{
					"id": "skill-1", "type": "function",
					"function": map[string]string{"name": "skill", "arguments": `{"skill":"story-style"}`},
				}},
			}
			finishReason = "tool_calls"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": finishReason}},
			"usage":   map[string]int{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18},
		})
	}))
	defer server.Close()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "story-style", "SKILL.md"), `---
name: story-style
description: Story style
---
Return the final storyboard.
`)
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Snapshot(context.Background(), "story-style")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(catalog, workspace, nil, 10, TrustedShellFactory)
	runtime.EnableModelTrace()

	result, err := runtime.Run(context.Background(), RunRequest{
		Skill: snapshot,
		Model: ModelConfig{BaseURL: server.URL, APIKey: "sk-secret", Model: "test-model"},
		Input: "create a storyboard",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "final storyboard" || calls != 2 {
		t.Fatalf("unexpected run result=%#v calls=%d", result, calls)
	}

	matches, err := filepath.Glob(filepath.Join(workspace.Root(), "*", "run-*", "model-trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one model trace, got %#v", matches)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-secret") {
		t.Fatal("model trace leaked the API key")
	}
	if !strings.Contains(string(raw), "create a storyboard") {
		t.Fatal("model trace did not preserve the plaintext business input")
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("trace lines=%d, want 2: %s", len(lines), raw)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first["status"] != float64(http.StatusOK) || first["model"] != "test-model" || first["request"] == nil || first["response"] == nil {
		t.Fatalf("unexpected first trace record: %#v", first)
	}
	if got := first["finishReasons"].([]any); len(got) != 1 || got[0] != "tool_calls" {
		t.Fatalf("unexpected finish reasons: %#v", got)
	}
	if got := first["toolCalls"].([]any); len(got) != 1 || got[0] != "skill" {
		t.Fatalf("unexpected tool calls: %#v", got)
	}
	usage := first["usage"].(map[string]any)
	if usage["promptTokens"] != float64(11) || usage["completionTokens"] != float64(7) {
		t.Fatalf("unexpected usage: %#v", usage)
	}
	request := first["request"].(map[string]any)
	messages := request["messages"].([]any)
	systemPrompt := messages[0].(map[string]any)["content"].(string)
	if !strings.Contains(systemPrompt, "# Skill 系统") ||
		!strings.Contains(systemPrompt, "你在一个已配置的 Agent Skill 下执行用户任务") {
		t.Fatalf("model request did not use Chinese system prompts: %q", systemPrompt)
	}
	toolNames := map[string]bool{}
	readToolDescription := ""
	tools := request["tools"].([]any)
	// 8 = 文件系统 6(ls/read/write/edit/glob/grep) + execute + skill;
	// deep 预装的 write_todos/task 已禁用(inline skill 链路纯开销)。
	if len(tools) != 8 {
		t.Fatalf("model request tool count=%d, want 8", len(tools))
	}
	for _, rawTool := range tools {
		function := rawTool.(map[string]any)["function"].(map[string]any)
		name := function["name"].(string)
		toolNames[name] = true
		if name == "read_file" {
			readToolDescription = function["description"].(string)
		}
	}
	if toolNames["write_todos"] || toolNames["task"] {
		t.Fatalf("deep 内置 write_todos/task 应已禁用: %#v", toolNames)
	}
	if !strings.Contains(readToolDescription, "从文件系统读取文件") {
		t.Fatalf("read_file tool was not localized to Chinese: %q", readToolDescription)
	}
}

// ExtraFields 是「后台在模型中心配了参数 → 上游真收到」这条链路的唯一出口，中间跨了
// eino 的 ChatModelConfig、go-openai 的 SetExtraFields、以及 marshaller 的 JSON MergePatch 三层。
// 断言结构体字段传下去了毫无意义——只有在 HTTP 这一端看到真实 body 才算数。
// 同时钉住 messages 没被 MergePatch 顺手改坏：它是 ExtraFields 最容易误伤的键。
func TestRuntimePassesExtraFieldsIntoUpstreamRequestBody(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		bodies = append(bodies, string(raw))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{
				"index": 0, "message": map[string]any{"role": "assistant", "content": "final storyboard"},
				"finish_reason": "stop",
			}},
		})
	}))
	defer server.Close()

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "story-style", "SKILL.md"), `---
name: story-style
description: Story style
---
Return the final storyboard.
`)
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Snapshot(context.Background(), "story-style")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(catalog, workspace, nil, 10, TrustedShellFactory)

	if _, err := runtime.Run(context.Background(), RunRequest{
		Skill: snapshot,
		Model: ModelConfig{
			BaseURL: server.URL, APIKey: "sk", Model: "test-model",
			ExtraFields: map[string]any{"reasoning_effort": "xhigh", "max_tokens": 4096},
		},
		Input: "create a storyboard",
	}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) == 0 {
		t.Fatal("upstream received no request")
	}

	var req struct {
		Model           string            `json:"model"`
		Messages        []json.RawMessage `json:"messages"`
		ReasoningEffort string            `json:"reasoning_effort"`
		MaxTokens       int               `json:"max_tokens"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &req); err != nil {
		t.Fatalf("decode upstream body %q: %v", bodies[0], err)
	}
	if req.ReasoningEffort != "xhigh" {
		t.Fatalf("reasoning_effort=%q, want xhigh; body=%s", req.ReasoningEffort, bodies[0])
	}
	if req.MaxTokens != 4096 {
		t.Fatalf("max_tokens=%d, want 4096; body=%s", req.MaxTokens, bodies[0])
	}
	if req.Model != "test-model" {
		t.Fatalf("model=%q, want test-model", req.Model)
	}
	if len(req.Messages) == 0 {
		t.Fatalf("messages was clobbered: %s", bodies[0])
	}
}

func TestRuntimeRejectsSkillDirectoryReplacedByEscapingSymlink(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "skills")
	skillDir := filepath.Join(root, "story-style")
	writeTestFile(t, filepath.Join(skillDir, "SKILL.md"), `---
name: story-style
description: Story style
---
Rules.
`)
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Snapshot(context.Background(), "story-style")
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(skillDir, skillDir+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, skillDir); err != nil {
		t.Fatal(err)
	}
	workspace, workspaceErr := NewRunWorkspace(t.TempDir())
	if workspaceErr != nil {
		t.Fatal(workspaceErr)
	}
	runtime := NewRuntime(catalog, workspace, func(context.Context, ModelConfig) (model.ToolCallingChatModel, error) {
		return &scriptedModel{state: &scriptedState{}}, nil
	}, 10, TrustedShellFactory)

	_, err = runtime.Run(context.Background(), RunRequest{
		Skill: snapshot, Model: ModelConfig{APIKey: "sk", Model: "test"}, Input: "input",
	})
	if err == nil || !strings.Contains(err.Error(), "outside the configured root") {
		t.Fatalf("expected symlink escape error, got %v", err)
	}
}

func TestRuntimeWritesSourceAnchorIntoRunWorkspace(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "story-style", "SKILL.md"), `---
name: story-style
description: Story style
---
Rules.
`)
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Snapshot(context.Background(), "story-style")
	if err != nil {
		t.Fatal(err)
	}
	fake := &scriptedModel{state: &scriptedState{}}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(catalog, workspace, func(context.Context, ModelConfig) (model.ToolCallingChatModel, error) {
		return fake, nil
	}, 12, TrustedShellFactory)

	if _, err := runtime.Run(context.Background(), RunRequest{
		Skill:      loaded,
		Model:      ModelConfig{BaseURL: "https://example.test/v1", APIKey: "sk", Model: "test"},
		Input:      "create a storyboard",
		SourceText: "第一句。\n第二句。",
	}); err != nil {
		t.Fatal(err)
	}

	matches, err := filepath.Glob(filepath.Join(workspace.Root(), "*", "run-*", "source.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one source.txt anchor, got %v", matches)
	}
	content, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "第一句。\n第二句。" {
		t.Fatalf("source.txt content mismatch: %q", content)
	}
}

func TestRuntimeSkipsSourceAnchorWhenSourceTextEmpty(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "story-style", "SKILL.md"), `---
name: story-style
description: Story style
---
Rules.
`)
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Snapshot(context.Background(), "story-style")
	if err != nil {
		t.Fatal(err)
	}
	fake := &scriptedModel{state: &scriptedState{}}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(catalog, workspace, func(context.Context, ModelConfig) (model.ToolCallingChatModel, error) {
		return fake, nil
	}, 12, TrustedShellFactory)

	if _, err := runtime.Run(context.Background(), RunRequest{
		Skill: loaded,
		Model: ModelConfig{BaseURL: "https://example.test/v1", APIKey: "sk", Model: "test"},
		Input: "create a storyboard",
	}); err != nil {
		t.Fatal(err)
	}

	matches, err := filepath.Glob(filepath.Join(workspace.Root(), "*", "run-*", "source.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("source.txt should not exist without SourceText, got %v", matches)
	}
}

type scriptedModel struct {
	tools []*schema.ToolInfo
	state *scriptedState
}

type scriptedState struct {
	calls                  int
	sawForcedSkill         bool
	sawSkillContent        bool
	sawWorkspaceRules      bool
	sawChineseSystemPrompt bool
	rootToolNames          map[string]bool
	rootSystemInstruction  string
	scenario               scriptedScenario
	artifactContent        string
	lsErrorFeedback        string
}

type scriptedScenario int

const (
	scriptedLegacy scriptedScenario = iota
	scriptedArtifactSuccess
	scriptedArtifactNotSubmitted
	scriptedArtifactSubmitFailure
	scriptedLsPathEscape
)

func (m *scriptedModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	clone := *m
	clone.tools = tools
	return &clone, nil
}

func (m *scriptedModel) Generate(_ context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	// WithTools returns request-local clones, so keep counters on the original through
	// the shared tool slice marker is not possible. Tests use a pointer receiver clone;
	// update the counter carried by the active clone and infer progression from history.
	options := model.GetCommonOptions(nil, opts...)
	hasSkill := false
	hasWriteFile := false
	for _, msg := range input {
		if msg.Role == schema.System && strings.Contains(msg.Content, "# Skill 系统") &&
			strings.Contains(msg.Content, "你在一个已配置的 Agent Skill 下执行用户任务") {
			m.state.sawChineseSystemPrompt = true
			m.state.rootSystemInstruction = msg.Content
		}
		if msg.Role == schema.Tool && msg.ToolName == "skill" {
			hasSkill = true
			if strings.Contains(msg.Content, "Style=warm") {
				m.state.sawSkillContent = true
			}
			if strings.Contains(msg.Content, "只能使用 candidate.json 这类相对路径") &&
				strings.Contains(msg.Content, "candidate.json") &&
				strings.Contains(msg.Content, "禁止使用 /tmp") {
				m.state.sawWorkspaceRules = true
			}
		}
		if msg.Role == schema.Tool && msg.ToolName == "write_file" {
			hasWriteFile = true
		}
	}
	m.state.rootToolNames = make(map[string]bool, len(options.Tools))
	for _, info := range options.Tools {
		m.state.rootToolNames[info.Name] = true
	}
	m.state.calls++
	if !hasSkill {
		// 激活阶段必须是 auto（Allowed 无白名单），绝不能回退到会被上游 400 的 forced 形态。
		m.state.sawForcedSkill = options.ToolChoice != nil && *options.ToolChoice == schema.ToolChoiceAllowed &&
			len(options.AllowedToolNames) == 0
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "skill-1", Type: "function",
			Function: schema.FunctionCall{Name: "skill", Arguments: `{"skill":"story-style"}`},
		}}), nil
	}
	switch m.state.scenario {
	case scriptedLsPathEscape:
		hasLsCall := false
		for _, msg := range input {
			if msg.Role == schema.Tool && msg.ToolName == "ls" {
				m.state.lsErrorFeedback = msg.Content
				return schema.AssistantMessage("final storyboard", nil), nil
			}
			for _, call := range msg.ToolCalls {
				if call.Function.Name == "ls" {
					hasLsCall = true
				}
			}
		}
		if hasLsCall {
			// ls 的 tool 结果没回来却又轮到模型说话——run 没把错误喂回来，直接终止。
			return schema.AssistantMessage("missing ls feedback", nil), nil
		}
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "ls-1", Type: "function",
			Function: schema.FunctionCall{Name: "ls", Arguments: `{"path":"/"}`},
		}}), nil
	case scriptedArtifactSuccess:
		if !hasWriteFile {
			args, _ := json.Marshal(map[string]string{"file_path": "candidate.json", "content": m.state.artifactContent})
			return schema.AssistantMessage("", []schema.ToolCall{{
				ID: "write-1", Type: "function",
				Function: schema.FunctionCall{Name: "write_file", Arguments: string(args)},
			}}), nil
		}
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "submit-1", Type: "function",
			Function: schema.FunctionCall{Name: submitArtifactToolName, Arguments: `{"path":"candidate.json"}`},
		}}), nil
	case scriptedArtifactNotSubmitted:
		return schema.AssistantMessage("ordinary assistant fallback", nil), nil
	case scriptedArtifactSubmitFailure:
		return schema.AssistantMessage("ordinary assistant fallback", []schema.ToolCall{{
			ID: "submit-missing", Type: "function",
			Function: schema.FunctionCall{Name: submitArtifactToolName, Arguments: `{"path":"candidate.json"}`},
		}}), nil
	}
	return schema.AssistantMessage("final storyboard", nil), nil
}

func (m *scriptedModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("unexpected streaming call")
}

var _ model.ToolCallingChatModel = (*scriptedModel)(nil)
