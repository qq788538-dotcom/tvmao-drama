// apps/api/internal/agent/skill_snapshot_test.go
//
// 钉死 skill 的会话级语义（Claude Code 式）：skill 指令全文快照随首条消息落库，
// 之后每一轮随历史回放自然带上。曾经 skill 是 per-message 的——第二轮起指令
// 彻底消失，多步骤 skill 跑完第一步就断片，用户回「继续」时模型手里根本没有
// 步骤清单。
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func skillForSnapshotTest() *Skill {
	return &Skill{
		ID:                "sk_script",
		Name:              "编剧大师",
		SkillType:         "force_tool",
		SystemPromptAddon: "创作严格按编号阶段推进：0 参数确认、1 素材问讯、2 选题策划。",
	}
}

func TestRun_SkillSnapshot_PersistedInSkillBlock(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{{{Role: schema.Assistant, Content: "好的"}}}}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_snap", ProjectID: 1, UserID: 1})

	a := &Agent{Planner: llm, Tools: newStubRegistryWith(), Repo: repo, Sink: &stubFrameSink{}}
	if err := a.Run(context.Background(), "thr_snap", "act_1", "开始", nil, skillForSnapshotTest()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	saved, _ := repo.ListMessages("thr_snap")
	var blocks []ContentBlock
	_ = json.Unmarshal(saved[0].ContentBlocks, &blocks)
	if len(blocks) == 0 || blocks[0].Type != "skill" {
		t.Fatalf("first block should be skill chip, got %+v", blocks)
	}
	if !strings.Contains(blocks[0].Content, "素材问讯") {
		t.Fatalf("skill block must carry the addon snapshot, got %q", blocks[0].Content)
	}
	// chip 渲染字段不受影响。
	if blocks[0].ID != "sk_script" || blocks[0].Name != "编剧大师" {
		t.Fatalf("chip fields broken: %+v", blocks[0])
	}
}

// 验收核心：第二轮**不带** skill 发消息，planner 的输入里仍必须有 skill 指令。
func TestRun_SkillSnapshot_SecondTurnStillSeesInstructions(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{
		{{Role: schema.Assistant, Content: "阶段 0：请确认参数"}},
		{{Role: schema.Assistant, Content: "阶段 1：素材问讯"}},
	}}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_multi", ProjectID: 1, UserID: 1})
	a := &Agent{Planner: llm, Tools: newStubRegistryWith(), Repo: repo, Sink: &stubFrameSink{}}

	if err := a.Run(context.Background(), "thr_multi", "act_1", "写个短片剧本", nil, skillForSnapshotTest()); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	// 第二轮：skill == nil（前端 chip 发完即清，per-message 携带）。
	if err := a.Run(context.Background(), "thr_multi", "act_2", "继续", nil, nil); err != nil {
		t.Fatalf("Run 2: %v", err)
	}

	llm.mu.Lock()
	defer llm.mu.Unlock()
	if len(llm.lastInputs) != 2 {
		t.Fatalf("expected 2 stream calls, got %d", len(llm.lastInputs))
	}
	assertHasSkillSystem := func(inputs []*schema.Message, turn string) {
		for _, m := range inputs {
			if m.Role == schema.System && strings.Contains(m.Content, "素材问讯") {
				return
			}
		}
		t.Fatalf("%s：planner 输入里没有 skill 指令（多步骤 skill 会在这里断片）", turn)
	}
	assertHasSkillSystem(llm.lastInputs[0], "第一轮")
	assertHasSkillSystem(llm.lastInputs[1], "第二轮")
	// 指令必须排在它所属的 user 消息之前（回放序）。
	firstTurn := llm.lastInputs[1]
	sysIdx, userIdx := -1, -1
	for i, m := range firstTurn {
		if sysIdx == -1 && m.Role == schema.System && strings.Contains(m.Content, "素材问讯") {
			sysIdx = i
		}
		if userIdx == -1 && m.Role == schema.User && strings.Contains(m.Content, "写个短片剧本") {
			userIdx = i
		}
	}
	if sysIdx == -1 || userIdx == -1 || sysIdx > userIdx {
		t.Fatalf("skill system message must precede its user message, got sys=%d user=%d", sysIdx, userIdx)
	}
}

// 老数据兼容：存量 skill block 没有 Content（当年只存 ID+Name 画 chip），回放
// 不得凭空造出空 System 消息。
func TestBuildHistory_LegacySkillBlockWithoutContent(t *testing.T) {
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_legacy", ProjectID: 1, UserID: 1})
	_ = repo.AppendMessage(msgWithBlocks("thr_legacy", "user", []ContentBlock{
		{Type: "skill", ID: "sk_old", Name: "旧技能"},
		{Type: "text", Text: "老消息"},
	}))

	a := &Agent{Repo: repo}
	msgs, err := a.buildHistory("thr_legacy")
	if err != nil {
		t.Fatalf("buildHistory: %v", err)
	}
	for _, m := range msgs {
		if m.Role == schema.System {
			t.Fatalf("legacy skill block must not produce a system message, got %q", m.Content)
		}
	}
}
