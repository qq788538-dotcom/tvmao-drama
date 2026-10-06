// apps/api/internal/agent/pairing_prune_test.go
//
// 钉死「thread 不许变砖」的三道防线：
//  1. 回放侧配对修剪（pruneUnpairedToolMessages）——兜住一切来源的孤儿配对；
//  2. 压缩切点校正——不把 assistant(tool_use)/tool 配对从中间切断；
//  3. 持久化入口合法化——非法 tool arguments 不再让整条 assistant 落成 []。
//
// 三者对应的历史事故：回放出孤儿 tool 消息 → 严格 backbone 400 → 该 thread
// 之后每次 Run 都失败，永远无法自愈。
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

func msgWithBlocks(threadID, role string, blocks []ContentBlock) Message {
	bj, _ := json.Marshal(blocks)
	return Message{ID: uuid.NewString(), ThreadID: threadID, Role: role, ContentBlocks: bj}
}

func TestBuildHistory_PrunesUnpairedToolPairs(t *testing.T) {
	repo := newInMemRepo()
	threadID := "thr_prune"
	_ = repo.CreateThread(Thread{ID: threadID, ProjectID: 1, UserID: 1})

	_ = repo.AppendMessage(makeMsg(threadID, "画两张图"))
	// 完整配对：assistant(tool_use c_ok) + tool(tool_result c_ok)。
	_ = repo.AppendMessage(msgWithBlocks(threadID, "assistant", []ContentBlock{
		{Type: "text", Text: "好的"},
		{Type: "tool_use", ID: "c_ok", Name: "generate_image", Input: json.RawMessage(`{}`)},
		// 孤儿 call：结果从未落库（进程被杀 / 落库失败）。
		{Type: "tool_use", ID: "c_orphan", Name: "generate_image", Input: json.RawMessage(`{}`)},
	}))
	_ = repo.AppendMessage(msgWithBlocks(threadID, "tool", []ContentBlock{
		{Type: "tool_result", ToolUseID: "c_ok", Content: "done"},
	}))
	// 孤儿 result：它的 assistant 半边丢了（Marshal 吞错年代落成 [] / 压缩切断）。
	_ = repo.AppendMessage(msgWithBlocks(threadID, "tool", []ContentBlock{
		{Type: "tool_result", ToolUseID: "c_lost", Content: "ghost"},
	}))
	// 整条 assistant 只剩孤儿 call、无文本 → 应整条消失。
	_ = repo.AppendMessage(msgWithBlocks(threadID, "assistant", []ContentBlock{
		{Type: "tool_use", ID: "c_orphan2", Name: "generate_image", Input: json.RawMessage(`{}`)},
	}))

	a := &Agent{Repo: repo}
	msgs, err := a.buildHistory(threadID)
	if err != nil {
		t.Fatalf("buildHistory: %v", err)
	}

	for _, m := range msgs {
		switch m.Role {
		case schema.Assistant:
			for _, tc := range m.ToolCalls {
				if tc.ID != "c_ok" {
					t.Fatalf("unpaired tool_call %q survived replay", tc.ID)
				}
			}
			if m.Content == "" && len(m.ToolCalls) == 0 {
				t.Fatal("empty assistant message survived replay")
			}
		case schema.Tool:
			if m.ToolCallID != "c_ok" {
				t.Fatalf("orphan tool_result %q survived replay（严格 backbone 会 400）", m.ToolCallID)
			}
		}
	}
	// 完整配对必须原样保留——修剪不能误伤。
	var sawCall, sawResult bool
	for _, m := range msgs {
		if m.Role == schema.Assistant {
			for _, tc := range m.ToolCalls {
				if tc.ID == "c_ok" {
					sawCall = true
				}
			}
		}
		if m.Role == schema.Tool && m.ToolCallID == "c_ok" {
			sawResult = true
		}
	}
	if !sawCall || !sawResult {
		t.Fatalf("intact pair was wrongly pruned: call=%v result=%v", sawCall, sawResult)
	}
}

// 压缩切点落在 tool 行上时必须向前挪到配对边界：宁可少压几条，也不能让保留区以
// 孤儿 tool 行开头。切点由 token 预算决定（window 的 20%），单一窗口值只能踩到
// 一个切点——所以扫一段窗口区间，保证无论切点算到哪，保留区都不以 tool 行开头。
func TestComputeCut_NeverStartsKeptRegionWithToolRow(t *testing.T) {
	big := strings.Repeat("x", 500)
	// 布局：user, assistant(tool_use c1), tool(c1), user, assistant(tool_use c2),
	// tool(c2), user×4 —— 两对配对散布在中段，扫窗口时切点会依次踩过每个位置。
	active := []Message{
		msgWithBlocks("t", "user", []ContentBlock{{Type: "text", Text: big}}),
		msgWithBlocks("t", "assistant", []ContentBlock{
			{Type: "text", Text: big},
			{Type: "tool_use", ID: "c1", Name: "generate_image", Input: json.RawMessage(`{}`)},
		}),
		msgWithBlocks("t", "tool", []ContentBlock{{Type: "tool_result", ToolUseID: "c1", Content: big}}),
		msgWithBlocks("t", "user", []ContentBlock{{Type: "text", Text: big}}),
		msgWithBlocks("t", "assistant", []ContentBlock{
			{Type: "text", Text: big},
			{Type: "tool_use", ID: "c2", Name: "generate_image", Input: json.RawMessage(`{}`)},
		}),
		msgWithBlocks("t", "tool", []ContentBlock{{Type: "tool_result", ToolUseID: "c2", Content: big}}),
		msgWithBlocks("t", "user", []ContentBlock{{Type: "text", Text: big}}),
		msgWithBlocks("t", "user", []ContentBlock{{Type: "text", Text: big}}),
		msgWithBlocks("t", "user", []ContentBlock{{Type: "text", Text: big}}),
		msgWithBlocks("t", "user", []ContentBlock{{Type: "text", Text: big}}),
	}

	cutsSeen := map[int]bool{}
	for window := 500; window <= 12000; window += 100 {
		cut := computeCut(active, window)
		cutsSeen[cut] = true
		if cut <= 0 {
			continue
		}
		if active[cut].Role == "tool" {
			t.Fatalf("window=%d: kept region starts with orphan tool row (cut=%d)", window, cut)
		}
	}
	// 反空转自检：窗口扫出的切点必须覆盖多个位置，否则上面的断言什么都没测到。
	if len(cutsSeen) < 4 {
		t.Fatalf("window sweep only produced cuts %v — test lost its coverage", cutsSeen)
	}
	// 大窗口保留更多（切点更小或相等）：token 预算随 window 缩放。
	if computeCut(active, 10000) > computeCut(active, 2000) {
		t.Fatalf("larger window must keep at least as much tail")
	}
}

// skill 指令快照绝不进摘要：它是 thread 的「已加载指令」，折进有损 summary 等于
// 把多阶段 skill 的方法论压没。带 skill block 的消息必须留在 active 区。
func TestCompressIfNeeded_SkillSnapshotNeverSummarized(t *testing.T) {
	repo := newInMemRepo()
	threadID := "thr_skill_keep"
	_ = repo.CreateThread(Thread{ID: threadID})

	big := strings.Repeat("x", 800)
	skillMsg := msgWithBlocks(threadID, "user", []ContentBlock{
		{Type: "skill", ID: "sk1", Name: "编剧大师", Content: "[Skill: 编剧大师]\n阶段状态机……"},
		{Type: "text", Text: big},
	})
	_ = repo.AppendMessage(skillMsg) // 最老的一条——裸切必然把它折进 summary
	for i := 0; i < 9; i++ {
		_ = repo.AppendMessage(makeMsg(threadID, big))
	}

	stub := &fakeSummarizer{out: "SUMMARY"}
	if err := CompressIfNeeded(context.Background(), repo, stub, threadID, 1024); err != nil {
		t.Fatalf("CompressIfNeeded: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("expected compression to run, calls=%d", stub.calls)
	}
	for _, m := range stub.lastMsgs {
		if m.ID == skillMsg.ID {
			t.Fatal("skill snapshot message was fed to summarizer")
		}
	}
	remaining, _ := repo.ListMessages(threadID)
	var skillAlive bool
	for _, m := range remaining {
		if m.ID == skillMsg.ID {
			skillAlive = true
		}
	}
	if !skillAlive {
		t.Fatal("skill snapshot message was marked summarized — 后续轮次将失去 skill 指令")
	}
}

// 非法 / 空 tool arguments 不再让 json.Marshal(blocks) 整体报错——曾经该错误被
// 吞掉，整条 assistant 落成 []。
func TestAssistantToBlocks_SanitizesInvalidToolArgs(t *testing.T) {
	m := &schema.Message{
		Role:    schema.Assistant,
		Content: "生成中",
		ToolCalls: []schema.ToolCall{
			{ID: "c_trunc", Function: schema.FunctionCall{Name: "generate_image", Arguments: `{"prompt":"被 max_tokens 截`}},
			{ID: "c_empty", Function: schema.FunctionCall{Name: "generate_image", Arguments: ""}},
			{ID: "c_good", Function: schema.FunctionCall{Name: "generate_image", Arguments: `{"prompt":"ok"}`}},
		},
	}
	blocks := assistantToBlocks(m, nil)
	if _, err := json.Marshal(blocks); err != nil {
		t.Fatalf("sanitized blocks must marshal cleanly, got %v", err)
	}
	for _, b := range blocks {
		if b.Type == "tool_use" && !json.Valid(b.Input) {
			t.Fatalf("tool_use %s Input still invalid JSON: %s", b.ID, b.Input)
		}
	}
	// 合法 arguments 必须原样保留，不能被合法化误伤。
	for _, b := range blocks {
		if b.ID == "c_good" && string(b.Input) != `{"prompt":"ok"}` {
			t.Fatalf("valid arguments were rewritten: %s", b.Input)
		}
	}
}

// 空 name 的 tool_call 校验现在发生在落库之前：DB 里只留文本部分，不留会变成
// 孤儿的 tool_use block。
func TestRun_EmptyToolCallName_PersistsTextOnly(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{{
		{Role: schema.Assistant, Content: "试试工具"},
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{ID: "c_noname", Function: schema.FunctionCall{Name: "", Arguments: `{}`}},
		}},
	}}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_noname", ProjectID: 1, UserID: 1})

	a := &Agent{Planner: llm, Tools: newStubRegistryWith(), Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_noname", "act_nn", "go", nil, nil); err == nil {
		t.Fatal("expected empty_tool_call error")
	}

	saved, _ := repo.ListMessages("thr_noname")
	if len(saved) != 2 {
		t.Fatalf("expected user + text-only assistant rows, got %d", len(saved))
	}
	var blocks []ContentBlock
	_ = json.Unmarshal(saved[1].ContentBlocks, &blocks)
	for _, b := range blocks {
		if b.Type == "tool_use" {
			t.Fatalf("broken tool_use must not be persisted, got %+v", b)
		}
	}
	ef := collectFatalTrio(t, sink.Frames(), "thr_noname", "act_nn")
	if ef.Error.Code != "empty_tool_call" {
		t.Fatalf("code = %q, want empty_tool_call", ef.Error.Code)
	}
}

// displayNameRegistry 是只关心 DisplayName 查询的最小 stub。
type displayNameRegistry struct {
	stubRegistry
	names map[string]string
}

func (d *displayNameRegistry) DisplayName(name string) string { return d.names[name] }

// tool_use 块要带上面向用户的显示名快照——查得到的填名字，查不到的留空
// （前端回退内部名），绝不因查不到而报错。
func TestAssistantToBlocks_DisplayName(t *testing.T) {
	m := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{ID: "c1", Function: schema.FunctionCall{Name: "generate_image", Arguments: `{}`}},
			{ID: "c2", Function: schema.FunctionCall{Name: "unknown_tool", Arguments: `{}`}},
		},
	}
	blocks := assistantToBlocks(m, &displayNameRegistry{names: map[string]string{"generate_image": "生成图片"}})
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d", len(blocks))
	}
	if blocks[0].DisplayName != "生成图片" {
		t.Fatalf("display name: %q", blocks[0].DisplayName)
	}
	if blocks[1].DisplayName != "" {
		t.Fatalf("unknown tool display name should be empty, got %q", blocks[1].DisplayName)
	}
}
