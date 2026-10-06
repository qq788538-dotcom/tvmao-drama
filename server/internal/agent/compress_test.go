// apps/api/internal/agent/compress_test.go
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// fakeSummarizer 记录调用次数 + 返回一个预设的 summary 字符串。
type fakeSummarizer struct {
	mu       sync.Mutex
	out      string
	err      error
	calls    int
	lastMsgs []Message
	lastPrev string
}

func (f *fakeSummarizer) Summarize(_ context.Context, existing string, msgs []Message) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastMsgs = append([]Message(nil), msgs...)
	f.lastPrev = existing
	return f.out, f.err
}

func TestEstimateTokens_Sanity(t *testing.T) {
	// 350 个 ASCII 字符，按 len/3.5 算 ≈ 100 token。
	s := ""
	for i := 0; i < 350; i++ {
		s += "a"
	}
	got := estimateTokens(s)
	if got < 90 || got > 110 {
		t.Fatalf("estimateTokens(350 ASCII) = %d, want ~100", got)
	}
	// 中文按 rune 计数，不按 byte：100 个中文字 ≈ 28 token。
	cn := ""
	for i := 0; i < 100; i++ {
		cn += "你"
	}
	cnGot := estimateTokens(cn)
	if cnGot < 20 || cnGot > 40 {
		t.Fatalf("estimateTokens(100 zh) = %d, want ~28", cnGot)
	}
}

// makeMsg 用给定的文本载荷构造一条 user 角色的消息——在各压缩测试里复用，让它们更紧凑。
func makeMsg(threadID, text string) Message {
	blocks, _ := json.Marshal([]ContentBlock{{Type: "text", Text: text}})
	return Message{
		ID:            uuid.NewString(),
		ThreadID:      threadID,
		Role:          "user",
		ContentBlocks: blocks,
	}
}

func TestCompressIfNeeded_BelowThreshold_NoOp(t *testing.T) {
	repo := newInMemRepo()
	threadID := "thr_below"
	_ = repo.CreateThread(Thread{ID: threadID})
	// 3 条短消息——远低于阈值。
	for i := 0; i < 3; i++ {
		_ = repo.AppendMessage(makeMsg(threadID, "hi"))
	}
	stub := &fakeSummarizer{out: "SHOULD-NOT-BE-USED"}
	if err := CompressIfNeeded(context.Background(), repo, stub, threadID, 100000); err != nil {
		t.Fatalf("CompressIfNeeded: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("summarizer should not be called below threshold, got calls=%d", stub.calls)
	}
	th, _ := repo.GetThread(threadID)
	if th.Summary != "" {
		t.Fatalf("expected empty summary, got %q", th.Summary)
	}
}

func TestCompressIfNeeded_SingleMessage_NoOp(t *testing.T) {
	repo := newInMemRepo()
	threadID := "thr_one"
	_ = repo.CreateThread(Thread{ID: threadID})
	// 只有一条消息时永远不压：最近一条是正在进行的回合，无条件保留；剩不下
	// 任何可折叠的东西。（旧的「≤6 条不压」按条数豁免已废除——5 条超大消息
	// 一样会撑爆 context，条数与内容量不挂钩。）
	big := ""
	for i := 0; i < 4000; i++ {
		big += "x"
	}
	_ = repo.AppendMessage(makeMsg(threadID, big))
	stub := &fakeSummarizer{out: "X"}
	if err := CompressIfNeeded(context.Background(), repo, stub, threadID, 1024); err != nil {
		t.Fatalf("CompressIfNeeded: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("expected no compression for a single-message thread, got calls=%d", stub.calls)
	}
}

// 旧行为的反向验证：少量但超大的消息（旧「≤6 条不压」的豁免区）现在必须压。
func TestCompressIfNeeded_FewHugeMessages_Compresses(t *testing.T) {
	repo := newInMemRepo()
	threadID := "thr_few_huge"
	_ = repo.CreateThread(Thread{ID: threadID})
	big := ""
	for i := 0; i < 4000; i++ {
		big += "x"
	}
	for i := 0; i < 5; i++ {
		_ = repo.AppendMessage(makeMsg(threadID, big))
	}
	stub := &fakeSummarizer{out: "X"}
	if err := CompressIfNeeded(context.Background(), repo, stub, threadID, 1024); err != nil {
		t.Fatalf("CompressIfNeeded: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("huge few-message thread must compress now, got calls=%d", stub.calls)
	}
	remaining, _ := repo.ListMessages(threadID)
	if len(remaining) == 0 || len(remaining) == 5 {
		t.Fatalf("expected partial fold, got %d remaining", len(remaining))
	}
}

func TestCompressIfNeeded_AboveThreshold_Compresses(t *testing.T) {
	repo := newInMemRepo()
	threadID := "thr_big"
	_ = repo.CreateThread(Thread{ID: threadID})
	// 10 条消息，每条都填充过，加起来 > 1024 window 的 75%。
	big := ""
	for i := 0; i < 500; i++ {
		big += "x"
	}
	ids := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		m := makeMsg(threadID, big)
		ids = append(ids, m.ID)
		_ = repo.AppendMessage(m)
	}
	stub := &fakeSummarizer{out: "SUMMARY"}
	if err := CompressIfNeeded(context.Background(), repo, stub, threadID, 1024); err != nil {
		t.Fatalf("CompressIfNeeded: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("expected 1 summarizer call, got %d", stub.calls)
	}
	// 尾巴按 token 预算（20% window=204 token）从新到旧纳入：每条 ~500 rune 的
	// 消息约 150 token，只装得下最后 1 条 → 前 9 条被折进 summary。被折的必须
	// 是最老的那批（前缀），保留的必须是最新的（后缀）。
	if len(stub.lastMsgs) != 9 {
		t.Fatalf("expected 9 messages to summarize (token tail keeps only the newest), got %d", len(stub.lastMsgs))
	}
	gotIDs := map[string]bool{}
	for _, m := range stub.lastMsgs {
		gotIDs[m.ID] = true
	}
	for i := 0; i < 9; i++ {
		if !gotIDs[ids[i]] {
			t.Fatalf("expected old message id %s in summarizer input", ids[i])
		}
	}
	// summary 持久化到 thread 上。
	th, _ := repo.GetThread(threadID)
	if th.Summary != "SUMMARY" {
		t.Fatalf("thread summary not persisted: %q", th.Summary)
	}
	remaining, _ := repo.ListMessages(threadID)
	if len(remaining) != 1 || remaining[0].ID != ids[9] {
		t.Fatalf("expected only the newest message to stay active, got %d", len(remaining))
	}
}

// failMarkRepo 包装 inMemRepo，让 MarkMessagesSummarized 定向失败，用来演练
// 「summary 已存、标记失败」这条半程失败路径。
type failMarkRepo struct {
	*inMemRepo
	markErr error
}

func (r *failMarkRepo) MarkMessagesSummarized(ids []string) error {
	if r.markErr != nil {
		return r.markErr
	}
	return r.inMemRepo.MarkMessagesSummarized(ids)
}

// TestCompressIfNeeded_MarkFail_RollsBackSummary 钉死半程失败的收口语义：
// UpdateThreadSummary 成功但 MarkMessagesSummarized 失败时，必须把 summary 回滚
// 到旧值。否则那批消息既进了 summary 又留在 active 历史——下次回放内容双份，
// 再压缩会把同批消息二次折叠进 summary。
func TestCompressIfNeeded_MarkFail_RollsBackSummary(t *testing.T) {
	inner := newInMemRepo()
	threadID := "thr_markfail"
	_ = inner.CreateThread(Thread{ID: threadID, Summary: "OLD-SUMMARY"})
	big := strings.Repeat("x", 500)
	for i := 0; i < 10; i++ {
		_ = inner.AppendMessage(makeMsg(threadID, big))
	}
	repo := &failMarkRepo{inMemRepo: inner, markErr: errors.New("db gone")}
	stub := &fakeSummarizer{out: "NEW-SUMMARY"}
	// 半程失败不冒泡——压缩绝不能卡住用户的一轮对话。
	if err := CompressIfNeeded(context.Background(), repo, stub, threadID, 1024); err != nil {
		t.Fatalf("expected nil error on mark failure, got %v", err)
	}
	// summary 必须回到旧值：消息全部还在 active（Mark 没成），summary 里若留着
	// NEW-SUMMARY 就是同批内容双份。
	th, _ := inner.GetThread(threadID)
	if th.Summary != "OLD-SUMMARY" {
		t.Fatalf("summary not rolled back after mark failure: %q", th.Summary)
	}
	remaining, _ := inner.ListMessages(threadID)
	if len(remaining) != 10 {
		t.Fatalf("expected all 10 messages still active, got %d", len(remaining))
	}
}

func TestCompressIfNeeded_NilSummarizer_NoOp(t *testing.T) {
	repo := newInMemRepo()
	threadID := "thr_nilsum"
	_ = repo.CreateThread(Thread{ID: threadID})
	big := ""
	for i := 0; i < 500; i++ {
		big += "x"
	}
	for i := 0; i < 10; i++ {
		_ = repo.AppendMessage(makeMsg(threadID, big))
	}
	// nil summarizer：压缩必须静默跳过，而不是空指针 panic。
	if err := CompressIfNeeded(context.Background(), repo, nil, threadID, 1024); err != nil {
		t.Fatalf("expected nil error with nil summarizer, got %v", err)
	}
	th, _ := repo.GetThread(threadID)
	if th.Summary != "" {
		t.Fatalf("expected empty summary, got %q", th.Summary)
	}
}

func TestCompressIfNeeded_SummarizerErr_NoOp(t *testing.T) {
	repo := newInMemRepo()
	threadID := "thr_err"
	_ = repo.CreateThread(Thread{ID: threadID})
	big := ""
	for i := 0; i < 500; i++ {
		big += "x"
	}
	for i := 0; i < 10; i++ {
		_ = repo.AppendMessage(makeMsg(threadID, big))
	}
	// summarizer 失败不应冒泡给调用方——记日志 + 跳过。
	stub := &fakeSummarizer{err: context.Canceled}
	if err := CompressIfNeeded(context.Background(), repo, stub, threadID, 1024); err != nil {
		t.Fatalf("expected nil error on summarizer failure, got %v", err)
	}
	th, _ := repo.GetThread(threadID)
	if th.Summary != "" {
		t.Fatalf("summary should not be persisted on summarizer error, got %q", th.Summary)
	}
	remaining, _ := repo.ListMessages(threadID)
	if len(remaining) != 10 {
		t.Fatalf("expected all 10 messages still active on summarizer error, got %d", len(remaining))
	}
}
