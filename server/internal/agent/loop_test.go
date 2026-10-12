// apps/api/internal/agent/loop_test.go
package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// fakeTool 是 loop 测试用的最小化 Tool。它的 InvokableRun 发出一个 image_info
// artifact，这样 TestRun_ToolCall_EmitsToolEvent 里对 tool_event 帧的断言才有东西
// 可观察。
type fakeTool struct{ name string }

func (f *fakeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: f.name,
		Desc: "fake tool for loop tests",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"prompt": {Type: schema.String, Desc: "prompt", Required: true},
		}),
	}, nil
}

func (f *fakeTool) InvokableRun(ctx context.Context, _ string, _ ...einotool.Option) (string, error) {
	EmitFromContext(ctx)(Artifact{ArtifactType: "image_info", ArtifactID: "art_fake"})
	return "ok", nil
}

// newStubRegistryWith 返回一个预先填好给定工具的 ToolRegistry，以它们的
// Info().Name 为 key。
func newStubRegistryWith(tools ...Tool) *stubRegistry {
	r := &stubRegistry{byName: map[string]Tool{}}
	for _, t := range tools {
		info, _ := t.Info(context.Background())
		r.byName[info.Name] = t
	}
	return r
}

// stubChatModel 返回一组固定的流式消息序列，每次 Stream 调用对应一个。实现了
// model.BaseChatModel；同时暴露了 BindTools，这样它满足 model.ChatModel，让 loop 能
// 跑到 BindTools 回退路径。lastInputs 捕获每次 Stream 调用的输入消息，这样测试能断言
// planner 实际看到了什么（被 Skill prompt-prepend 测试用到）。
type stubChatModel struct {
	chunks     [][]*schema.Message // 外层：每次 Stream 调用。内层：该次 stream 的各 chunk。
	mu         sync.Mutex
	calls      int
	lastInputs [][]*schema.Message
}

func (s *stubChatModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.calls
	s.calls++
	if idx >= len(s.chunks) || len(s.chunks[idx]) == 0 {
		return &schema.Message{Role: schema.Assistant}, nil
	}
	last := s.chunks[idx][len(s.chunks[idx])-1]
	return last, nil
}

func (s *stubChatModel) Stream(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	s.mu.Lock()
	idx := s.calls
	s.calls++
	captured := make([]*schema.Message, len(in))
	copy(captured, in)
	s.lastInputs = append(s.lastInputs, captured)
	s.mu.Unlock()

	var chunks []*schema.Message
	if idx < len(s.chunks) {
		chunks = s.chunks[idx]
	}
	sr, sw := schema.Pipe[*schema.Message](len(chunks) + 1)
	go func() {
		defer sw.Close()
		for _, c := range chunks {
			sw.Send(c, nil)
		}
	}()
	return sr, nil
}

// BindTools 是一个宽松的 no-op，让 loop 的 BindTools 回退路径被跑到，同时不用让
// stub 关心实际的工具。
func (s *stubChatModel) BindTools(_ []*schema.ToolInfo) error { return nil }

// stubFrameSink 捕获一次 Run 期间 loop 发出的所有帧。
type stubFrameSink struct {
	mu     sync.Mutex
	frames []Frame
}

func (s *stubFrameSink) Send(f Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = append(s.frames, f)
}

func (s *stubFrameSink) Frames() []Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Frame, len(s.frames))
	copy(out, s.frames)
	return out
}

// inMemRepo 是给 loop 测试用的内存版 Repository——让单元测试免去 MySQL 搭建。
type inMemRepo struct {
	mu       sync.Mutex
	threads  map[string]*Thread
	messages map[string][]Message
}

func newInMemRepo() *inMemRepo {
	return &inMemRepo{
		threads:  map[string]*Thread{},
		messages: map[string][]Message{},
	}
}

func (r *inMemRepo) LoadedSkillIDs(threadID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0)
	for _, m := range r.messages[threadID] {
		if m.SummarizedAt.Valid {
			continue
		}
		out = append(out, skillIDsFromBlocks(m.ContentBlocks)...)
	}
	return out, nil
}

func (r *inMemRepo) CreateThread(t Thread) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	tt := t
	r.threads[t.ID] = &tt
	return nil
}

func (r *inMemRepo) GetThread(id string) (*Thread, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.threads[id]; ok {
		return t, nil
	}
	return &Thread{ID: id}, nil
}

func (r *inMemRepo) ListThreadsByProject(int64) ([]Thread, error) { return nil, nil }
func (r *inMemRepo) UpdateThreadStatus(string, string) error      { return nil }
func (r *inMemRepo) UpdateThreadTitle(string, string) error       { return nil }
func (r *inMemRepo) DeleteThread(string) error                    { return nil }

func (r *inMemRepo) UpdateThreadSummary(threadID, summary string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.threads[threadID]
	if !ok {
		t = &Thread{ID: threadID}
		r.threads[threadID] = t
	}
	t.Summary = summary
	return nil
}

func (r *inMemRepo) AppendMessage(m Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages[m.ThreadID] = append(r.messages[m.ThreadID], m)
	return nil
}

func (r *inMemRepo) ListMessages(id string) ([]Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	all := r.messages[id]
	out := make([]Message, 0, len(all))
	for _, m := range all {
		if m.SummarizedAt.Valid {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// ListMessagesPageBefore 在活跃消息的内存序（即插入序）上做与 MySQL 实现同语义
// 的开窗：无游标取尾部 limit 条；有游标取游标前 limit 条；游标不在活跃集则视为
// 失效返回空页。loop 的测试不用它，但 fake 必须满足接口。
func (r *inMemRepo) ListMessagesPageBefore(threadID, beforeID string, limit int) ([]Message, bool, error) {
	active, err := r.ListMessages(threadID)
	if err != nil {
		return nil, false, err
	}
	end := len(active)
	if beforeID != "" {
		end = -1
		for i, m := range active {
			if m.ID == beforeID {
				end = i
				break
			}
		}
		if end < 0 {
			return []Message{}, false, nil
		}
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	return active[start:end], start > 0, nil
}

func (r *inMemRepo) MarkMessagesSummarized(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	now := sql.NullTime{Time: time.Now(), Valid: true}
	for tid, msgs := range r.messages {
		for i := range msgs {
			if set[msgs[i].ID] {
				msgs[i].SummarizedAt = now
			}
		}
		r.messages[tid] = msgs
	}
	return nil
}

func TestRun_TextOnlyResponse_NoTool(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{
		{{Role: schema.Assistant, Content: "你好"}},
	}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_1", ProjectID: 1, UserID: 1})

	a := &Agent{Planner: llm, Tools: newStubRegistryWith(&fakeTool{name: "fake_image"}), Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_1", "act_1", "say hi", nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	frames := sink.Frames()
	if !containsFrameType(frames, FrameActionEnd) {
		t.Fatalf("want action_end frame, got %v", frameTypes(frames))
	}
	if frames[len(frames)-1].Type != FrameThreadEnd {
		t.Fatalf("last frame should be thread_end, got %s", frames[len(frames)-1].Type)
	}
	saved, _ := repo.ListMessages("thr_1")
	if len(saved) != 2 || saved[0].Role != "user" || saved[1].Role != "assistant" {
		t.Fatalf("messages wrong: %+v", saved)
	}
	if saved[1].StopReason != "end_turn" {
		t.Fatalf("expected end_turn stop reason, got %q", saved[1].StopReason)
	}
}

func TestRun_ToolCall_EmitsToolEvent(t *testing.T) {
	toolCalls := []schema.ToolCall{{
		ID:   "toolu_1",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "fake_image",
			Arguments: `{"prompt":"shiba"}`,
		},
	}}
	llm := &stubChatModel{chunks: [][]*schema.Message{
		{{Role: schema.Assistant, ToolCalls: toolCalls}}, // 第 1 轮：tool_use
		{{Role: schema.Assistant, Content: "已生成"}},       // 第 2 轮：tool result 之后的文本
	}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_1", ProjectID: 1, UserID: 1})

	a := &Agent{Planner: llm, Tools: newStubRegistryWith(&fakeTool{name: "fake_image"}), Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_1", "act_1", "画一只柴犬", nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	frames := sink.Frames()
	if !containsToolEvent(frames) {
		t.Fatalf("want tool_event frame, got: %v", frameTypes(frames))
	}
	if frames[len(frames)-1].Type != FrameThreadEnd {
		t.Fatalf("last frame should be thread_end, got %s", frames[len(frames)-1].Type)
	}

	// 验证两条 assistant 消息之间持久化了一条 tool result。
	saved, _ := repo.ListMessages("thr_1")
	var sawToolMsg bool
	for _, m := range saved {
		if m.Role == "tool" {
			sawToolMsg = true
			break
		}
	}
	if !sawToolMsg {
		t.Fatalf("expected a tool message in repo, got roles: %v", roles(saved))
	}
}

func TestRun_MaxRoundsGuard(t *testing.T) {
	tc := []schema.ToolCall{{
		ID:       "tu1",
		Type:     "function",
		Function: schema.FunctionCall{Name: "fake_image", Arguments: `{"prompt":"x"}`},
	}}
	rounds := make([][]*schema.Message, 20)
	for i := range rounds {
		rounds[i] = []*schema.Message{{Role: schema.Assistant, ToolCalls: tc}}
	}
	llm := &stubChatModel{chunks: rounds}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_1", ProjectID: 1, UserID: 1})

	a := &Agent{Planner: llm, Tools: newStubRegistryWith(&fakeTool{name: "fake_image"}), Repo: repo, Sink: sink, MaxRounds: 3}
	err := a.Run(context.Background(), "thr_1", "act_1", "loop me", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "max rounds") {
		t.Fatalf("want max rounds error, got %v", err)
	}
	// 必须发出 max_rounds 的 error 帧，且随后跟着 action_end(error)+thread_end
	// 这组确定的终止帧——否则前端没有终止信号、会永久转圈（sendFatal 的契约）。
	frames := sink.Frames()
	var errIdx = -1
	for i, f := range frames {
		if f.Type == FrameError && f.Error != nil && f.Error.Code == "max_rounds" {
			errIdx = i
			break
		}
	}
	if errIdx < 0 {
		t.Fatalf("expected an error frame with code=max_rounds, frames=%+v", frames)
	}
	if errIdx+2 >= len(frames) ||
		frames[errIdx+1].Type != FrameActionEnd || frames[errIdx+1].StopReason != "error" ||
		frames[errIdx+2].Type != FrameThreadEnd {
		t.Fatalf("expected error frame followed by action_end(error)+thread_end, got %+v", frames[errIdx:])
	}
}

// TestRun_WithForceToolSkill_RestrictsToolSet 验证当一个 Skill 把 force_tools 钉到
// 一个子集时，agent 本次 Run 的工具集会相应地被过滤。LLM stub 对允许的工具发一个
// tool_call，而注册的 fake_video 工具保持原封不动。严格的可观察信号是为允许的工具
// 发出的 tool_event 帧；第二个工具的名字从不出现在任何帧里。
func TestRun_WithForceToolSkill_RestrictsToolSet(t *testing.T) {
	toolCalls := []schema.ToolCall{{
		ID:   "toolu_skill_1",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "fake_image",
			Arguments: `{"prompt":"shiba"}`,
		},
	}}
	llm := &stubChatModel{chunks: [][]*schema.Message{
		{{Role: schema.Assistant, ToolCalls: toolCalls}},
		{{Role: schema.Assistant, Content: "done"}},
	}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_1", ProjectID: 1, UserID: 1})

	// Registry 有两个工具；skill 限制到只有 fake_image。
	reg := newStubRegistryWith(&fakeTool{name: "fake_image"}, &fakeTool{name: "fake_video"})
	skill := &Skill{
		ID:         "img_only",
		Name:       "Image Only",
		ForceTools: []string{"fake_image"},
	}

	a := &Agent{Planner: llm, Tools: reg, Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_1", "act_1", "test", nil, skill); err != nil {
		t.Fatalf("Run: %v", err)
	}

	frames := sink.Frames()
	if !containsToolEvent(frames) {
		t.Fatalf("want tool_event frame for fake_image, got: %v", frameTypes(frames))
	}
	// 不应有任何帧引用 fake_video。
	for _, f := range frames {
		if f.Block != nil && f.Block.Name == "fake_video" {
			t.Fatalf("fake_video should be excluded from skill tool set, but appeared in frame: %+v", f)
		}
	}
}

// TestRun_WithSkillPromptAddon_PrependsSystem 验证 skill 的 SystemPromptAddon 会作为
// prompt 的第一条消息（Role=schema.System）送达 planner。
func TestRun_WithSkillPromptAddon_PrependsSystem(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{
		{{Role: schema.Assistant, Content: "ok"}},
	}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_1", ProjectID: 1, UserID: 1})

	skill := &Skill{
		ID:                "haiku_skill",
		Name:              "Haiku Mode",
		SystemPromptAddon: "Always reply in haiku",
	}

	a := &Agent{Planner: llm, Tools: newStubRegistryWith(&fakeTool{name: "fake_image"}), Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_1", "act_1", "hi", nil, skill); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(llm.lastInputs) == 0 {
		t.Fatalf("expected at least one Stream call captured")
	}
	first := llm.lastInputs[0]
	if len(first) == 0 {
		t.Fatalf("expected non-empty input messages")
	}
	// msgs[0] 是全局 system prompt（永远在最前，见 prompt.go 的位置约定）；
	// skill addon 是另一条 System 消息，由历史回放渲染，排在它后面。
	if first[0].Role != schema.System {
		t.Fatalf("first message role should be system, got %q", first[0].Role)
	}
	if !hasSystemContaining(first, "haiku") {
		t.Fatalf("system message should contain skill addon 'haiku', got %+v", first)
	}
}

// hasSystemContaining 报告消息序列里是否有一条 System 消息含指定子串。
// 断言「以 System 送达」而不是「在第几条」——全局 system prompt 落位之后，
// skill addon 不再是 msgs[0]，钉下标的断言会随任何一次前缀调整集体作废。
func hasSystemContaining(msgs []*schema.Message, sub string) bool {
	for _, m := range msgs {
		if m.Role == schema.System && strings.Contains(m.Content, sub) {
			return true
		}
	}
	return false
}

// TestRun_WithMentionImage_BuildsMultimodalUserMessage 验证当 mentions 带 URL 时，
// 送给 planner 的最后一条输入消息是用多模态 part（text + image_url）重建的 User 消息。
func TestRun_WithMentionImage_BuildsMultimodalUserMessage(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{{{Role: schema.Assistant, Content: "OK"}}}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_1", ProjectID: 1, UserID: 1})

	a := &Agent{Planner: llm, Tools: newStubRegistryWith(), Repo: repo, Sink: sink}
	mentions := []Mention{{ArtifactID: "art_1", URL: "http://x/1.png", Name: "柴犬"}}
	if err := a.Run(context.Background(), "thr_1", "act_1", "redraw this", mentions, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	captured := llm.lastInputs[0]
	last := captured[len(captured)-1]
	if last.Role != schema.User {
		t.Fatalf("last msg should be user, got %s", last.Role)
	}
	if len(last.MultiContent) < 2 {
		t.Fatalf("expected multimodal parts, got %+v", last)
	}
	foundImage := false
	for _, p := range last.MultiContent {
		if p.Type == schema.ChatMessagePartTypeImageURL && p.ImageURL != nil && p.ImageURL.URL == "http://x/1.png" {
			foundImage = true
		}
	}
	if !foundImage {
		t.Fatalf("image part not found: %+v", last.MultiContent)
	}
}

// fakeSigner 用可逆的前缀替换模拟 MediaSigner：Sign 把 media://<key> 换成
// http://signed.example/<key>?sig=fresh，Canonicalize 反向剥掉签名。外部 URL
// 两个方向都原样透传——跟真实 MediaSigner 的宽容语义一致。
type fakeSigner struct{}

func (fakeSigner) Sign(s string) string {
	if strings.HasPrefix(s, "media://") {
		return "http://signed.example/" + strings.TrimPrefix(s, "media://") + "?sig=fresh"
	}
	return s
}

func (fakeSigner) Canonicalize(s string) string {
	if !strings.HasPrefix(s, "http://signed.example/") {
		return s
	}
	rest := strings.TrimPrefix(s, "http://signed.example/")
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest = rest[:i]
	}
	return "media://" + rest
}

// TestRun_ToolUseInput_CanonicalizedOnPersist 钉死「持久化一律 media:// canonical」
// 的不变量也覆盖 tool_use 的 Input：planner 拿 mention 的签名 URL 当工具参数时，
// 落库的 arguments 不许留下 24h 会过期的签名 URL。曾经 canonicalizeBlocks 只处理
// Text/Content，Input 原样落库。
func TestRun_ToolUseInput_CanonicalizedOnPersist(t *testing.T) {
	signedURL := "http://signed.example/pics/shiba.png?sig=fresh"
	toolCalls := []schema.ToolCall{{
		ID:   "toolu_sig",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "fake_image",
			Arguments: `{"prompt":"redraw","image_url":"` + signedURL + `"}`,
		},
	}}
	llm := &stubChatModel{chunks: [][]*schema.Message{
		{{Role: schema.Assistant, ToolCalls: toolCalls}},
		{{Role: schema.Assistant, Content: "done"}},
	}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_sig", ProjectID: 1, UserID: 1})

	a := &Agent{Planner: llm, Tools: newStubRegistryWith(&fakeTool{name: "fake_image"}), Repo: repo, Sink: sink, Signer: fakeSigner{}}
	if err := a.Run(context.Background(), "thr_sig", "act_1", "redraw", nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	saved, _ := repo.ListMessages("thr_sig")
	var input string
	for _, m := range saved {
		if m.Role != "assistant" {
			continue
		}
		var blocks []ContentBlock
		_ = json.Unmarshal(m.ContentBlocks, &blocks)
		for _, b := range blocks {
			if b.Type == "tool_use" {
				input = string(b.Input)
			}
		}
	}
	if input == "" {
		t.Fatal("no tool_use block persisted")
	}
	if strings.Contains(input, "signed.example") {
		t.Fatalf("persisted tool_use Input still holds a signed URL (will expire in 24h): %s", input)
	}
	if !strings.Contains(input, "media://pics/shiba.png") {
		t.Fatalf("persisted tool_use Input missing canonical media:// URL: %s", input)
	}
	// 子串替换绝不能破坏 JSON 形态——Input 落库后还要被回放成 tool_call arguments。
	if !json.Valid(json.RawMessage(input)) {
		t.Fatalf("persisted tool_use Input is no longer valid JSON: %s", input)
	}
}

// TestBuildHistory_ResignsToolUseInput 是上一个测试的回放侧对称面：DB 里 tool_use
// Input 存的是 media:// canonical 形态，buildHistory 回放时必须重签成新鲜的 https
// ——LLM 没法对 media:// 伪 URL 做 fetch 或推理。曾经 signHistoryBlocks 只重签
// Text/Content/URL，Input 里的 media:// 原样喂给 planner。
func TestBuildHistory_ResignsToolUseInput(t *testing.T) {
	repo := newInMemRepo()
	threadID := "thr_resign"
	_ = repo.CreateThread(Thread{ID: threadID, ProjectID: 1, UserID: 1})
	_ = repo.AppendMessage(makeMsg(threadID, "redraw"))
	_ = repo.AppendMessage(msgWithBlocks(threadID, "assistant", []ContentBlock{
		{Type: "tool_use", ID: "c1", Name: "fake_image", Input: json.RawMessage(`{"image_url":"media://pics/shiba.png"}`)},
	}))
	_ = repo.AppendMessage(msgWithBlocks(threadID, "tool", []ContentBlock{
		{Type: "tool_result", ToolUseID: "c1", Content: "ok"},
	}))
	// 没有 Input 的 tool_use（老数据/空 arguments）不许 panic、不许被改写出内容。
	_ = repo.AppendMessage(msgWithBlocks(threadID, "assistant", []ContentBlock{
		{Type: "text", Text: "以及一个没参数的调用"},
		{Type: "tool_use", ID: "c2", Name: "fake_image"},
	}))
	_ = repo.AppendMessage(msgWithBlocks(threadID, "tool", []ContentBlock{
		{Type: "tool_result", ToolUseID: "c2", Content: "ok"},
	}))

	a := &Agent{Repo: repo, Signer: fakeSigner{}}
	msgs, err := a.buildHistory(threadID)
	if err != nil {
		t.Fatalf("buildHistory: %v", err)
	}
	var args string
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if tc.ID == "c1" {
				args = tc.Function.Arguments
			}
		}
	}
	if args == "" {
		t.Fatal("tool_call c1 not replayed")
	}
	if strings.Contains(args, "media://") {
		t.Fatalf("replayed tool_call arguments still hold media:// (LLM can't use it): %s", args)
	}
	if !strings.Contains(args, "http://signed.example/pics/shiba.png") {
		t.Fatalf("replayed tool_call arguments missing fresh signed URL: %s", args)
	}
}

func containsFrameType(fs []Frame, t FrameType) bool {
	for _, f := range fs {
		if f.Type == t {
			return true
		}
	}
	return false
}

func containsToolEvent(fs []Frame) bool {
	for _, f := range fs {
		if f.Block != nil && f.Block.Type == "tool_event" {
			return true
		}
	}
	return false
}

func frameTypes(fs []Frame) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, string(f.Type))
	}
	return out
}

func roles(ms []Message) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Role)
	}
	return out
}

// TestRun_WorkflowSkill_ExecutesSteps 验证一个 skill_type=workflow 的 skill 会完全
// 绕过 LLM 循环，转而通过 ExecuteWorkflow 驱动声明式的 WorkflowSteps。{{input.prompt}}
// 替换应该取到用户的文本，并且标准的帧 envelope（thread_start → action_start →
// step_start/step_end → action_end → thread_end）必须包住整个 workflow。
func TestRun_WorkflowSkill_ExecutesSteps(t *testing.T) {
	toolA := &recordingTool{name: "wf_tool_a", emitURL: "http://x/a.png"}
	reg := newStubRegistryWith(toolA)
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_1", ProjectID: 1, UserID: 1})

	skill := &Skill{
		ID:            "wf_test",
		Name:          "WF Test",
		SkillType:     "workflow",
		WorkflowSteps: json.RawMessage(`[{"name":"only","tool_name":"wf_tool_a","inputs":{"prompt":"{{input.prompt}}"}}]`),
	}
	a := &Agent{Planner: &stubChatModel{}, Tools: reg, Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_1", "act_1", "draw shiba", nil, skill); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if toolA.lastArgs == "" || !strings.Contains(toolA.lastArgs, "draw shiba") {
		t.Fatalf("tool_a args missing prompt: %q", toolA.lastArgs)
	}
	types := frameTypes(sink.Frames())
	if !containsSubseq(types, []string{"thread_start", "action_start", "step_start", "step_end", "action_end", "thread_end"}) {
		t.Fatalf("frame order: %v", types)
	}
	// 应该已经持久化了一条合成的 assistant 消息，这样后续轮次能在历史里看到这次
	// workflow 运行。
	saved, _ := repo.ListMessages("thr_1")
	if len(saved) != 2 || saved[0].Role != "user" || saved[1].Role != "assistant" {
		t.Fatalf("expected user + assistant rows, got roles %v", roles(saved))
	}
	if saved[1].StopReason != "end_turn" {
		t.Fatalf("expected synthetic assistant stop_reason=end_turn, got %q", saved[1].StopReason)
	}
}

// TestBuildHistory_PrependsSummary 验证当一个 thread 有非空 summary（因为之前某一轮
// CompressIfNeeded 把更老的消息折进去了）时，buildHistory 会把它作为 planner 输入的
// 第一条 System 消息注入进去。
func TestBuildHistory_PrependsSummary(t *testing.T) {
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_sum", ProjectID: 1, UserID: 1, Summary: "earlier we agreed X"})
	// 后面跟着一条未被 summarize 的 user 消息。
	_ = repo.AppendMessage(makeMsg("thr_sum", "继续"))

	a := &Agent{Repo: repo}
	msgs, err := a.buildHistory("thr_sum")
	if err != nil {
		t.Fatalf("buildHistory: %v", err)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != schema.System {
		t.Fatalf("first msg should be system, got %s", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "earlier we agreed X") {
		t.Fatalf("system content missing summary: %q", msgs[0].Content)
	}
}

// TestRun_CallsCompressIfNeeded 验证当 active 历史冲破了配置的 context window 时，
// Run 会调用 summarizer。
func TestRun_CallsCompressIfNeeded(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{{{Role: schema.Assistant, Content: "ok"}}}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_cmp", ProjectID: 1, UserID: 1})

	// 给 thread 塞够多的胖消息，这样当前这轮 user 消息追加进来后就能触发阈值。
	big := strings.Repeat("x", 500)
	for i := 0; i < 10; i++ {
		_ = repo.AppendMessage(makeMsg("thr_cmp", big))
	}
	stub := &fakeSummarizer{out: "compressed-summary"}
	a := &Agent{
		Planner:              llm,
		Tools:                newStubRegistryWith(),
		Repo:                 repo,
		Sink:                 sink,
		PlannerContextWindow: 1024,
		Summarizer:           stub,
	}
	if err := a.Run(context.Background(), "thr_cmp", "act_cmp", "再来", nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("expected summarizer to be called once, got %d", stub.calls)
	}
	th, _ := repo.GetThread("thr_cmp")
	if th.Summary != "compressed-summary" {
		t.Fatalf("thread summary not persisted: %q", th.Summary)
	}
}

// TestRun_MetaSkill_RestrictsToMetaTool 验证一个 skill_type=meta 的 skill 仍然驱动
// LLM 循环（而非 workflow 路径），且工具集被强制为只有内置的 output_skill_definition
// 工具。skill 的 SystemPromptAddon 必须落在 planner 输入的第一条消息。
func TestRun_MetaSkill_RestrictsToMetaTool(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{{{Role: schema.Assistant, Content: "ok"}}}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_1", ProjectID: 1, UserID: 1})

	// Registry 通过 ByName/Snapshot 暴露真正的内置 meta 工具。
	reg := newStubRegistryWith(&OutputSkillDefinitionTool{})
	skill := &Skill{
		ID:                "meta_skill",
		Name:              "Meta",
		SkillType:         "meta",
		SystemPromptAddon: "create skill draft",
	}
	a := &Agent{Planner: llm, Tools: reg, Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_1", "act_1", "make me a skill", nil, skill); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 验证 LLM 被调用了——也就是说我们没有意外进到 workflow 路径。
	if len(llm.lastInputs) == 0 {
		t.Fatal("LLM never invoked")
	}
	// prompt addon 必须以 System 消息送达。不钉下标：msgs[0] 现在是全局 system
	// prompt，skill addon 排在它后面（由历史回放渲染）。
	if !hasSystemContaining(llm.lastInputs[0], "create skill draft") {
		t.Fatalf("system prompt missing: %+v", llm.lastInputs[0])
	}
}

// barrierTool 用来证明同一回合的 tool_call 是并发执行的：每次 InvokableRun 先报到
// （arrived），再阻塞等待 release。测试侧凑齐 N 个报到后才 close(release)。若执行是
// 串行的，第 2 个工具永远不会启动 → 第 1 个在 barrier 上死等 → Run 卡住（测试超时）。
type barrierTool struct {
	name    string
	arrived chan struct{}
	release chan struct{}
}

func (b *barrierTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: b.name, Desc: "barrier tool"}, nil
}

func (b *barrierTool) InvokableRun(ctx context.Context, _ string, _ ...einotool.Option) (string, error) {
	b.arrived <- struct{}{}
	select {
	case <-b.release:
		return "done", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// TestRun_ParallelToolFanout 验证同一回合的多个 tool_call 并发执行（靠 barrier 死锁
// 检测），且结果按 tool_call 原始顺序回灌持久化。
func TestRun_ParallelToolFanout(t *testing.T) {
	const n = 3
	bt := &barrierTool{name: "fake_image", arrived: make(chan struct{}, n), release: make(chan struct{})}
	go func() {
		for i := 0; i < n; i++ {
			<-bt.arrived
		}
		close(bt.release)
	}()

	calls := []schema.ToolCall{
		{ID: "toolu_0", Type: "function", Function: schema.FunctionCall{Name: "fake_image", Arguments: `{"prompt":"a"}`}},
		{ID: "toolu_1", Type: "function", Function: schema.FunctionCall{Name: "fake_image", Arguments: `{"prompt":"b"}`}},
		{ID: "toolu_2", Type: "function", Function: schema.FunctionCall{Name: "fake_image", Arguments: `{"prompt":"c"}`}},
	}
	llm := &stubChatModel{chunks: [][]*schema.Message{
		{{Role: schema.Assistant, ToolCalls: calls}},
		{{Role: schema.Assistant, Content: "都好了"}},
	}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_1", ProjectID: 1, UserID: 1})
	a := &Agent{Planner: llm, Tools: newStubRegistryWith(bt), Repo: repo, Sink: sink}

	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background(), "thr_1", "act_1", "并行三个", nil, nil) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run 卡住——工具没有并行执行（串行会在 barrier 上死锁）")
	}

	// 三条 tool_result 必须按 tool_call 原始顺序持久化。
	saved, _ := repo.ListMessages("thr_1")
	var toolBlocks []ContentBlock
	for _, m := range saved {
		if m.Role == "tool" {
			_ = json.Unmarshal(m.ContentBlocks, &toolBlocks)
			break
		}
	}
	var gotIDs []string
	for _, b := range toolBlocks {
		if b.Type == "tool_result" {
			gotIDs = append(gotIDs, b.ToolUseID)
		}
	}
	want := []string{"toolu_0", "toolu_1", "toolu_2"}
	if len(gotIDs) != len(want) {
		t.Fatalf("want %d tool_result blocks %v, got %v", len(want), want, gotIDs)
	}
	for i := range want {
		if gotIDs[i] != want[i] {
			t.Fatalf("tool_result 顺序错乱：want %v, got %v", want, gotIDs)
		}
	}
}

// TestRun_PrependsGlobalSystemPrompt 验证每次 Run 都把全局 system prompt 作为
// msgs[0] 送达 planner——**包括不选 skill 的裸对话**。这是这条改动的核心：在此
// 之前不带 skill 的 Run 一条 System 消息都没有。
func TestRun_PrependsGlobalSystemPrompt(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{
		{{Role: schema.Assistant, Content: "ok"}},
	}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_gp", ProjectID: 1, UserID: 1})

	a := &Agent{Planner: llm, Tools: newStubRegistryWith(), Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_gp", "act_1", "hi", nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(llm.lastInputs) == 0 {
		t.Fatalf("expected at least one Stream call captured")
	}
	msgs := llm.lastInputs[0]
	if len(msgs) == 0 || msgs[0].Role != schema.System {
		t.Fatalf("msgs[0] should be the global system prompt, got %+v", msgs)
	}
	if msgs[0].Content != GlobalSystemPrompt {
		t.Fatalf("msgs[0] should be verbatim GlobalSystemPrompt, got %q", msgs[0].Content)
	}
	// 花钱前必须先问——这条是用户拍板的产品铁律，不是可有可无的措辞。整段
	// prompt 里别的句子怎么改都行，这条语义丢了就是回归。
	for _, kw := range []string{"执行生成前必须先问", "不要自己估算或编造数字", "我是小喵"} {
		if !strings.Contains(msgs[0].Content, kw) {
			t.Fatalf("global prompt lost key rule %q", kw)
		}
	}
}

// TestRun_GlobalPromptPrecedesThreadSummary 钉死顺序：全局 prompt 在前、thread
// summary 在后。顺序不只是观感——内容永不变的前缀放最前，才谈得上被上游的
// 前缀缓存命中（见 prompt.go 的位置约定）。
func TestRun_GlobalPromptPrecedesThreadSummary(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{
		{{Role: schema.Assistant, Content: "ok"}},
	}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_gs", ProjectID: 1, UserID: 1, Summary: "earlier we agreed X"})

	a := &Agent{Planner: llm, Tools: newStubRegistryWith(), Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_gs", "act_1", "继续", nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	msgs := llm.lastInputs[0]
	if len(msgs) < 2 {
		t.Fatalf("expected global prompt + summary + user, got %d", len(msgs))
	}
	if msgs[0].Content != GlobalSystemPrompt {
		t.Fatalf("msgs[0] should be global prompt, got %q", msgs[0].Content)
	}
	if msgs[1].Role != schema.System || !strings.Contains(msgs[1].Content, "earlier we agreed X") {
		t.Fatalf("msgs[1] should be the thread summary, got %+v", msgs[1])
	}
}

// TestRunWorkflow_SkipsGlobalPrompt 守护 workflow 分支不受影响：它根本不调 planner，
// 全局 prompt 自然一次也不该被组装。分流点在 Run 顶部（skill_type == "workflow"），
// 这条测试钉住那个分流没有被后来的改动挪到 prompt 注入之后。
func TestRunWorkflow_SkipsGlobalPrompt(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{
		{{Role: schema.Assistant, Content: "should not be called"}},
	}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_wf", ProjectID: 1, UserID: 1})

	steps, _ := json.Marshal([]WorkflowStep{{Name: "step1", ToolName: "fake_image", Inputs: map[string]any{"prompt": "x"}}})
	skill := &Skill{
		ID:            "wf",
		Name:          "WF",
		SkillType:     "workflow",
		WorkflowSteps: steps,
	}
	a := &Agent{Planner: llm, Tools: newStubRegistryWith(&fakeTool{name: "fake_image"}), Repo: repo, Sink: sink}
	_ = a.Run(context.Background(), "thr_wf", "act_1", "go", nil, skill)

	if len(llm.lastInputs) != 0 {
		t.Fatalf("workflow path must not invoke planner, got %d Stream calls", len(llm.lastInputs))
	}
}

// ctxErrModel：ctx 已取消时 Stream 直接报 ctx.Err()，模拟用户点停止后
// LLM 流被掐断的形态。
type ctxErrModel struct{ stubChatModel }

func (m *ctxErrModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return m.stubChatModel.Stream(ctx, in, opts...)
}

// 用户点停止（abort 帧掐 Run ctx）不是故障：收尾必须是 code=aborted 的中性
// 三件套（error + action_end + thread_end），前端据此渲染「已停止」而不是红色
// 报错。计费边界由既有机制保证（settle 走 Background ctx），这里只钉收尾语义。
func TestRun_CancelledCtx_EndsWithAbortedCode(t *testing.T) {
	llm := &ctxErrModel{}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_abort", ProjectID: 1, UserID: 1})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 模拟 abort 帧已把 Run ctx 掐掉
	a := &Agent{Planner: llm, Tools: newStubRegistryWith(&fakeTool{name: "fake_image"}), Repo: repo, Sink: sink}
	if err := a.Run(ctx, "thr_abort", "act_1", "写个剧本", nil, nil); err == nil {
		t.Fatal("cancelled run must return error")
	}

	var gotAborted, gotActionEnd bool
	for _, f := range sink.frames {
		if f.Type == FrameError && f.Error != nil {
			if f.Error.Code != "aborted" {
				t.Fatalf("error code = %q, want aborted", f.Error.Code)
			}
			gotAborted = true
		}
		if f.Type == FrameActionEnd {
			gotActionEnd = true
		}
	}
	if !gotAborted || !gotActionEnd {
		t.Fatalf("want aborted error + action_end, frames: %+v", sink.frames)
	}
}

// midStreamErrModel：先吐一段正文，再在流中途报错——new-api 上游过载时的真实形态
// （HTTP 200 后推 "servers are currently overloaded"）。
type midStreamErrModel struct{ stubChatModel }

func (m *midStreamErrModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, sw := schema.Pipe[*schema.Message](2)
	go func() {
		defer sw.Close()
		sw.Send(&schema.Message{Role: schema.Assistant, Content: "你好！有什么"}, nil)
		sw.Send(nil, errors.New("Our servers are currently overloaded"))
	}()
	return sr, nil
}

// 用户已经看到的半截回复必须落库：否则轮次结束后的 re-hydrate 会把它从界面抹掉。
func TestRun_MidStreamError_PersistsPartialText(t *testing.T) {
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_mid", ProjectID: 1, UserID: 1})
	a := &Agent{Planner: &midStreamErrModel{}, Tools: newStubRegistryWith(&fakeTool{name: "fake_image"}), Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_mid", "act_1", "你好", nil, nil); err == nil {
		t.Fatal("mid-stream error must fail the run")
	}

	msgs, _ := repo.ListMessages("thr_mid")
	var got *Message
	for i := range msgs {
		if msgs[i].Role == "assistant" {
			got = &msgs[i]
		}
	}
	if got == nil {
		t.Fatalf("partial assistant text not persisted, msgs: %+v", msgs)
	}
	if got.StopReason != "error" || !strings.Contains(string(got.ContentBlocks), "你好！有什么") {
		t.Fatalf("unexpected persisted row: stop=%q blocks=%s", got.StopReason, got.ContentBlocks)
	}
}
