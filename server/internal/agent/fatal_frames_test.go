// apps/api/internal/agent/fatal_frames_test.go
//
// 钉死「终止帧三件套」的契约：error 帧必须带 ThreadID/ActionID。前端 handleFrame
// 在进任何分支之前先按 thread_id 路由，缺字段的帧被整帧丢弃——曾经全部 8 处
// FrameError 发射点都漏了这两个字段，导致错误气泡分支是死代码、部分路径只能等
// 21min 看门狗。这组测试在修复缺失时会红（帧上 ThreadID 为空串）。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// collectFatalTrio 在帧序列里定位 error / action_end / thread_end 三件套并断言
// 关键字段。返回 error 帧供调用方做 code/message 断言。
func collectFatalTrio(t *testing.T, frames []Frame, wantThread, wantAction string) Frame {
	t.Helper()
	var errFrame *Frame
	var sawActionEnd, sawThreadEnd bool
	for i := range frames {
		f := frames[i]
		switch f.Type {
		case FrameError:
			errFrame = &frames[i]
		case FrameActionEnd:
			if f.StopReason == "error" {
				sawActionEnd = true
				if f.ThreadID != wantThread || f.ActionID != wantAction {
					t.Fatalf("action_end ids = (%q,%q), want (%q,%q)", f.ThreadID, f.ActionID, wantThread, wantAction)
				}
			}
		case FrameThreadEnd:
			sawThreadEnd = true
		}
	}
	if errFrame == nil {
		t.Fatalf("no error frame in %v", frameTypes(frames))
	}
	if errFrame.ThreadID != wantThread {
		t.Fatalf("error frame thread_id = %q, want %q（缺 thread_id 的 error 帧会被前端整帧丢弃）", errFrame.ThreadID, wantThread)
	}
	if errFrame.ActionID != wantAction {
		t.Fatalf("error frame action_id = %q, want %q", errFrame.ActionID, wantAction)
	}
	if !sawActionEnd || !sawThreadEnd {
		t.Fatalf("fatal trio incomplete: action_end(error)=%v thread_end=%v in %v", sawActionEnd, sawThreadEnd, frameTypes(frames))
	}
	return *errFrame
}

func TestSendFatal_FramesCarryThreadAndAction(t *testing.T) {
	sink := &stubFrameSink{}
	SendFatal(sink, "thr_f", "act_f", "boom", "it broke")
	ef := collectFatalTrio(t, sink.Frames(), "thr_f", "act_f")
	if ef.Error == nil || ef.Error.Code != "boom" {
		t.Fatalf("error payload = %+v", ef.Error)
	}
}

// 空 workflow_steps 曾经只发一个裸 error 帧：没有 thread_id（前端丢弃）也没有
// action_end（转圈无解）。修复后必须是完整三件套。
func TestRun_WorkflowEmptySteps_SendsFatalTrio(t *testing.T) {
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_we", ProjectID: 1, UserID: 1})
	skill := &Skill{ID: "wf_empty", Name: "空 WF", SkillType: "workflow", WorkflowSteps: json.RawMessage(`[]`)}

	a := &Agent{Planner: &stubChatModel{}, Tools: newStubRegistryWith(), Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_we", "act_we", "hi", nil, skill); err == nil {
		t.Fatal("expected error for empty workflow_steps")
	}
	ef := collectFatalTrio(t, sink.Frames(), "thr_we", "act_we")
	if ef.Error.Code != "workflow_empty" {
		t.Fatalf("code = %q, want workflow_empty", ef.Error.Code)
	}
}

// step 失败必须以带原因的 error 帧透出（step_end 帧不携带错误信息），且合成的
// assistant 消息要写实际执行数，不能把半途而废写成全部跑完。
func TestRun_WorkflowStepFailure_ErrorFrameAndHonestSummary(t *testing.T) {
	failing := &recordingTool{name: "wf_fail", err: errors.New("upstream 500")}
	reg := newStubRegistryWith(failing)
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_wf", ProjectID: 1, UserID: 1})
	skill := &Skill{
		ID: "wf_fail", Name: "会失败的 WF", SkillType: "workflow",
		WorkflowSteps: json.RawMessage(`[{"name":"s1","tool_name":"wf_fail","inputs":{}},{"name":"s2","tool_name":"wf_fail","inputs":{}}]`),
	}

	a := &Agent{Planner: &stubChatModel{}, Tools: reg, Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_wf", "act_wf", "go", nil, skill); err == nil {
		t.Fatal("expected error from failing step")
	}

	ef := collectFatalTrio(t, sink.Frames(), "thr_wf", "act_wf")
	if ef.Error.Code != "workflow_step" {
		t.Fatalf("code = %q, want workflow_step", ef.Error.Code)
	}
	if !strings.Contains(ef.Error.Message, "upstream 500") {
		t.Fatalf("error message should carry step failure reason, got %q", ef.Error.Message)
	}

	saved, _ := repo.ListMessages("thr_wf")
	if len(saved) != 2 {
		t.Fatalf("expected user + synthetic assistant rows, got %d", len(saved))
	}
	var blocks []ContentBlock
	_ = json.Unmarshal(saved[1].ContentBlocks, &blocks)
	if len(blocks) == 0 || !strings.Contains(blocks[0].Text, "failed after 0/2") {
		t.Fatalf("synthetic summary should state actual executed count, got %+v", blocks)
	}
	if saved[1].StopReason != "error" {
		t.Fatalf("stop_reason = %q, want error", saved[1].StopReason)
	}
}
