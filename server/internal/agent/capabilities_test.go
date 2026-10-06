// apps/api/internal/agent/capabilities_test.go
//
// 对标 Seko 的能力补齐（G1/G3/G4/3.1）的行为契约：
//   - create_text_node：文本产物以 text_info artifact 落画布，聊天里不复读全文；
//   - load_skill：技能指令以 tool_result 进历史，跨轮持续生效（与 S1 同机制）；
//   - @ 视频/音频：不再挂成 image part（上游只认图片），转成文本引用；
//   - 流式块序号：整个 action 内单调递增，多轮回复不再实时互相覆盖。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// ---- G1 create_text_node ----

func TestCreateTextNodeTool_EmitsTextArtifact(t *testing.T) {
	var got []Artifact
	ctx := WithEmit(context.Background(), func(a Artifact) { got = append(got, a) })

	tool := &CreateTextNodeTool{}
	out, err := tool.InvokableRun(ctx, `{"title":"短片剧本《暴雨单》","text":"# 第一场\n外景，暴雨。"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(got))
	}
	a := got[0]
	if a.ArtifactType != "text_info" || a.Name != "短片剧本《暴雨单》" || !strings.Contains(a.Text, "第一场") {
		t.Fatalf("artifact mismatch: %+v", a)
	}
	if a.ArtifactID == "" {
		t.Fatal("artifact id required（前端定位 chip 靠它找节点）")
	}
	// 返回值必须引导 LLM 不在聊天里复读全文（Seko 行为：「已写入画布，请查阅」）。
	if !strings.Contains(out, "不要在聊天里重复正文") {
		t.Fatalf("tool result should instruct no-repeat, got %q", out)
	}
}

func TestCreateTextNodeTool_EmptyTextRejected(t *testing.T) {
	tool := &CreateTextNodeTool{}
	if _, err := tool.InvokableRun(context.Background(), `{"title":"空","text":"  "}`); err == nil {
		t.Fatal("empty text must be rejected")
	}
}

// ---- G3 load_skill ----

type stubSkillsRepo struct {
	byID map[string]*Skill
}

func (r *stubSkillsRepo) Get(id string) (*Skill, error) {
	if s, ok := r.byID[id]; ok {
		return s, nil
	}
	return nil, errors.New("not found")
}
func (r *stubSkillsRepo) Create(Skill) error  { return nil }
func (r *stubSkillsRepo) Update(Skill) error  { return nil }
func (r *stubSkillsRepo) Delete(string) error { return nil }
func (r *stubSkillsRepo) List() ([]Skill, error) {
	out := make([]Skill, 0, len(r.byID))
	for _, s := range r.byID {
		out = append(out, *s)
	}
	return out, nil
}
func (r *stubSkillsRepo) ListPublicByCategory(string) ([]Skill, error) { return nil, nil }
func (r *stubSkillsRepo) ListForUser(int64) ([]Skill, error)           { return nil, nil }
func (r *stubSkillsRepo) ListFeatured() ([]Skill, error)               { return nil, nil }
func (r *stubSkillsRepo) IncrementUseCount(string) error               { return nil }

func TestLoadSkillTool_LoadsByIDOrName(t *testing.T) {
	repo := &stubSkillsRepo{byID: map[string]*Skill{
		"director-master": {ID: "director-master", Name: "导演大师", SkillType: "force_tool", SystemPromptAddon: "镜头语言方法论……"},
	}}
	tool := &LoadSkillTool{Skills: repo}

	for _, q := range []string{"director-master", "导演大师"} {
		out, err := tool.InvokableRun(context.Background(), `{"skill":"`+q+`"}`)
		if err != nil {
			t.Fatalf("InvokableRun(%q): %v", q, err)
		}
		if !strings.Contains(out, "镜头语言方法论") || !strings.Contains(out, "持续生效") {
			t.Fatalf("loaded instructions missing for %q: %q", q, out)
		}
	}
}

func TestLoadSkillTool_NotFoundListsCandidates(t *testing.T) {
	repo := &stubSkillsRepo{byID: map[string]*Skill{
		"a": {ID: "a", Name: "编剧大师", SkillType: "force_tool", SystemPromptAddon: "x"},
	}}
	tool := &LoadSkillTool{Skills: repo}
	out, err := tool.InvokableRun(context.Background(), `{"skill":"不存在的"}`)
	// 找不到返回可读文本而非 error——error 会以「Tool failed」形态喂回 planner，
	// 模型容易放弃；带候选清单的文本让它自纠。
	if err != nil {
		t.Fatalf("not-found should not be an error, got %v", err)
	}
	if !strings.Contains(out, "编剧大师") {
		t.Fatalf("candidates missing: %q", out)
	}
}

func TestLoadSkillTool_WorkflowTypeRejected(t *testing.T) {
	repo := &stubSkillsRepo{byID: map[string]*Skill{
		"wf": {ID: "wf", Name: "宏", SkillType: "workflow", SystemPromptAddon: "x"},
	}}
	tool := &LoadSkillTool{Skills: repo}
	out, err := tool.InvokableRun(context.Background(), `{"skill":"wf"}`)
	if err != nil || !strings.Contains(out, "workflow") {
		t.Fatalf("workflow skill must be politely refused, out=%q err=%v", out, err)
	}
}

// ---- G4 mention 分流 ----

func TestBuildUserMessage_VideoAudioBecomeTextRefs(t *testing.T) {
	msg := buildUserMessage("看看这个", []Mention{
		{ArtifactID: "n1", URL: "https://x/img.png", Kind: "image", Name: "图1"},
		{ArtifactID: "n2", URL: "https://x/clip", Kind: "video", Name: "片段2"},
		{ArtifactID: "n3", URL: "https://x/voice", Kind: "audio", Name: "配音3"},
	})
	// 只有图片能挂 image part。
	imgParts := 0
	var textPart string
	for _, p := range msg.MultiContent {
		switch p.Type {
		case schema.ChatMessagePartTypeImageURL:
			imgParts++
			if p.ImageURL.URL != "https://x/img.png" {
				t.Fatalf("unexpected image part %q（视频/音频挂成 image part 会被上游拒绝）", p.ImageURL.URL)
			}
		case schema.ChatMessagePartTypeText:
			textPart = p.Text
		}
	}
	if imgParts != 1 {
		t.Fatalf("expected exactly 1 image part, got %d", imgParts)
	}
	// 视频/音频转成文本引用，且视频提示 analyze_video。
	for _, want := range []string{"片段2", "https://x/clip", "analyze_video", "配音3", "https://x/voice"} {
		if !strings.Contains(textPart, want) {
			t.Fatalf("text refs missing %q in %q", want, textPart)
		}
	}
}

func TestBuildUserMessage_LegacyNoKindStillImagePart(t *testing.T) {
	// 旧客户端没有 kind 字段——必须保持原行为（挂 image part），不能倒退。
	msg := buildUserMessage("hi", []Mention{{ArtifactID: "n1", URL: "https://x/a.png"}})
	if len(msg.MultiContent) != 2 || msg.MultiContent[1].Type != schema.ChatMessagePartTypeImageURL {
		t.Fatalf("legacy mention must stay an image part: %+v", msg.MultiContent)
	}
}

func TestRun_MentionKindPersistedAsTypedBlock(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{{{Role: schema.Assistant, Content: "好"}}}}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_mk", ProjectID: 1, UserID: 1})
	a := &Agent{Planner: llm, Tools: newStubRegistryWith(), Repo: repo, Sink: &stubFrameSink{}}
	err := a.Run(context.Background(), "thr_mk", "act_mk", "看视频", []Mention{
		{ArtifactID: "n2", URL: "https://x/clip", Kind: "video", Name: "片段"},
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	saved, _ := repo.ListMessages("thr_mk")
	var blocks []ContentBlock
	_ = json.Unmarshal(saved[0].ContentBlocks, &blocks)
	var found bool
	for _, b := range blocks {
		if b.Type == "video" && b.URL == "https://x/clip" {
			found = true
		}
		if b.Type == "image" && b.URL == "https://x/clip" {
			t.Fatal("video mention persisted as image block（历史界面 <img> 渲染视频 URL 会破图）")
		}
	}
	if !found {
		t.Fatalf("typed video block missing: %+v", blocks)
	}
}

// ---- G5 audio_info ----

func TestDynamicTool_AudioCategoryEmitsAudioInfo(t *testing.T) {
	var got []Artifact
	ctx := WithEmit(context.Background(), func(a Artifact) { got = append(got, a) })
	tool := &DynamicTool{Category: "audio"}
	tool.emitURLs(ctx, []string{"https://x/voice.mp3"})
	if len(got) != 1 || got[0].ArtifactType != "audio_info" {
		t.Fatalf("expected audio_info artifact, got %+v", got)
	}
}

// ---- 3.1 流式块序号 ----

func TestRun_StreamBlockIndicesMonotonicAcrossRounds(t *testing.T) {
	// 两轮：第一轮文本+工具调用，第二轮纯文本收口。曾经第二轮文本块 index 又
	// 从 0 起算，前端实时视图里覆盖第一轮的文本。
	llm := &stubChatModel{chunks: [][]*schema.Message{
		{
			{Role: schema.Assistant, Content: "先画一张"},
			{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "c1", Function: schema.FunctionCall{Name: "fake_tool", Arguments: `{"prompt":"x"}`}}}},
		},
		{{Role: schema.Assistant, Content: "画好了"}},
	}}
	reg := newStubRegistryWith(&fakeTool{name: "fake_tool"})
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_idx", ProjectID: 1, UserID: 1})

	a := &Agent{Planner: llm, Tools: reg, Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_idx", "act_idx", "画", nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	seen := map[int]bool{}
	lastStart := -1
	for _, f := range sink.Frames() {
		if f.Type != FrameContentBlockStart || f.Block == nil {
			continue
		}
		// tool_event 帧不带 Index（挂在 Block 上语义不同），只检查 text/tool_use 块。
		if f.Block.Type != "text" && f.Block.Type != "tool_use" {
			continue
		}
		if seen[f.Index] {
			t.Fatalf("block index %d reused within one action（第二轮会覆盖第一轮的实时内容）", f.Index)
		}
		seen[f.Index] = true
		if f.Index < lastStart {
			t.Fatalf("block index went backwards: %d after %d", f.Index, lastStart)
		}
		lastStart = f.Index
	}
	if len(seen) < 3 {
		t.Fatalf("expected >=3 distinct blocks (text+tool_use+text), got %v", seen)
	}
}

// ---- create_gen_node（铺节点不生成，零扣费）----

func TestCreateGenNodeTool_EmitsPrefilledNodeArtifact(t *testing.T) {
	var got []Artifact
	ctx := WithEmit(context.Background(), func(a Artifact) { got = append(got, a) })
	tool := &CreateGenNodeTool{}

	out, err := tool.InvokableRun(ctx, `{"kind":"video","prompt":"雨夜追逐，手持镜头","title":"镜头1"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if len(got) != 1 || got[0].ArtifactType != "gen_video_node_info" {
		t.Fatalf("expected gen_video_node_info artifact, got %+v", got)
	}
	if got[0].Prompt != "雨夜追逐，手持镜头" || got[0].Name != "镜头1" {
		t.Fatalf("prompt/title mismatch: %+v", got[0])
	}
	// 铁律：这条链路绝不自动扣积分——返回值必须引导 LLM 告知用户手动生成。
	if !strings.Contains(out, "未生成）") || !strings.Contains(out, "手动点击生成") {
		t.Fatalf("tool result must state no-generation semantics, got %q", out)
	}

	got = got[:0]
	if _, err := tool.InvokableRun(ctx, `{"kind":"image","prompt":"雪地柴犬"}`); err != nil {
		t.Fatalf("image kind: %v", err)
	}
	if len(got) != 1 || got[0].ArtifactType != "gen_image_node_info" {
		t.Fatalf("expected gen_image_node_info artifact, got %+v", got)
	}
}

func TestCreateGenNodeTool_RejectsBadArgs(t *testing.T) {
	tool := &CreateGenNodeTool{}
	if _, err := tool.InvokableRun(context.Background(), `{"kind":"gif","prompt":"x"}`); err == nil {
		t.Fatal("unknown kind must be rejected")
	}
	if _, err := tool.InvokableRun(context.Background(), `{"kind":"image","prompt":"  "}`); err == nil {
		t.Fatal("empty prompt must be rejected")
	}
}
