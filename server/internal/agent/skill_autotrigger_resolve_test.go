package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// autoStubSkills 是本组测试专用的 SkillsRepo 替身：只需要 ListForUser 有货。
type autoStubSkills struct {
	list    []Skill
	listErr error
	bumped  []string
}

func (r *autoStubSkills) Get(string) (*Skill, error)                   { return nil, errors.New("nope") }
func (r *autoStubSkills) Create(Skill) error                           { return nil }
func (r *autoStubSkills) Update(Skill) error                           { return nil }
func (r *autoStubSkills) Delete(string) error                          { return nil }
func (r *autoStubSkills) List() ([]Skill, error)                       { return r.list, nil }
func (r *autoStubSkills) ListPublicByCategory(string) ([]Skill, error) { return nil, nil }
func (r *autoStubSkills) ListForUser(int64) ([]Skill, error)           { return r.list, r.listErr }
func (r *autoStubSkills) ListFeatured() ([]Skill, error)               { return nil, nil }
func (r *autoStubSkills) IncrementUseCount(id string) error {
	r.bumped = append(r.bumped, id)
	return nil
}

func withSkillBlock(t *testing.T, repo *inMemRepo, threadID, skillID string) {
	t.Helper()
	if err := repo.CreateThread(Thread{ID: threadID}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	b, _ := json.Marshal([]ContentBlock{{Type: "skill", ID: skillID, Name: skillID}})
	if err := repo.AppendMessage(Message{ID: skillID + "-msg", ThreadID: threadID, Role: "user", ContentBlocks: b}); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func TestResolveAutoSkill_命中并标记为自动(t *testing.T) {
	skills := &autoStubSkills{list: []Skill{
		{ID: "writer", SkillType: "force_tool", TriggerKeywords: []string{"剧本"}, ForceTools: []string{"create_text_node"}},
	}}
	repo := newInMemRepo()
	got := ResolveAutoSkill(1, "thr", "帮我写个剧本", skills, repo)
	if got == nil || got.ID != "writer" {
		t.Fatalf("want writer, got %v", got)
	}
	if !got.AutoTriggered {
		t.Fatal("命中的 skill 必须标记 AutoTriggered，否则前端分不清是不是用户自己选的")
	}
	// 自动触发不套用 ForceTools：误触发的代价只能是"多了段指令"，不能是工具被静默裁掉。
	if got.ForceTools != nil {
		t.Fatalf("ForceTools 必须被清空，got %v", got.ForceTools)
	}
}

func TestResolveAutoSkill_已加载过的跳过(t *testing.T) {
	skills := &autoStubSkills{list: []Skill{
		{ID: "writer", SkillType: "force_tool", TriggerKeywords: []string{"剧本"}},
	}}
	repo := newInMemRepo()
	withSkillBlock(t, repo, "thr", "writer")
	if got := ResolveAutoSkill(1, "thr", "再改改剧本", skills, repo); got != nil {
		t.Fatalf("已挂载过的技能不该重复挂，got %v", got)
	}
}

func TestResolveAutoSkill_已加载A不挡B(t *testing.T) {
	skills := &autoStubSkills{list: []Skill{
		{ID: "writer", SkillType: "force_tool", TriggerKeywords: []string{"剧本"}},
		{ID: "director", SkillType: "force_tool", TriggerKeywords: []string{"分镜"}},
	}}
	repo := newInMemRepo()
	withSkillBlock(t, repo, "thr", "writer")
	got := ResolveAutoSkill(1, "thr", "把这些镜头分镜一下", skills, repo)
	if got == nil || got.ID != "director" {
		t.Fatalf("want director, got %v", got)
	}
}

func TestResolveAutoSkill_读技能目录失败不阻塞(t *testing.T) {
	skills := &autoStubSkills{listErr: errors.New("db down")}
	if got := ResolveAutoSkill(1, "thr", "写个剧本", skills, newInMemRepo()); got != nil {
		t.Fatalf("目录读不到就当没有自动触发，got %v", got)
	}
}

func TestResolveAutoSkill_依赖为nil时安全(t *testing.T) {
	if got := ResolveAutoSkill(1, "thr", "写个剧本", nil, nil); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
	skills := &autoStubSkills{list: []Skill{{ID: "w", SkillType: "force_tool", TriggerKeywords: []string{"剧本"}}}}
	// repo 为 nil 时拿不到"已加载"名单，退化成"可能重复挂一次"而不是完全不触发。
	if got := ResolveAutoSkill(1, "thr", "写个剧本", skills, nil); got == nil {
		t.Fatal("repo 为 nil 不该让自动触发整个失效")
	}
}

// 自动触发的 skill 落进 skill block 时必须带 auto 标记：用户没点过任何东西，
// 气泡上却凭空出现一个 chip，不标出来他只会困惑"这标签哪来的"。
func TestRun_自动触发的skill块带auto标记(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{{{Role: schema.Assistant, Content: "好的"}}}}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_auto", ProjectID: 1, UserID: 1})

	sk := skillForSnapshotTest()
	sk.AutoTriggered = true
	a := &Agent{Planner: llm, Tools: newStubRegistryWith(), Repo: repo, Sink: &stubFrameSink{}}
	if err := a.Run(context.Background(), "thr_auto", "act_1", "写个剧本", nil, sk); err != nil {
		t.Fatalf("Run: %v", err)
	}
	saved, _ := repo.ListMessages("thr_auto")
	var blocks []ContentBlock
	_ = json.Unmarshal(saved[0].ContentBlocks, &blocks)
	if len(blocks) == 0 || blocks[0].Type != "skill" {
		t.Fatalf("want skill block, got %+v", blocks)
	}
	if !blocks[0].Auto {
		t.Fatal("自动触发的 skill block 必须带 auto=true")
	}
}

func TestRun_手动选的skill块不带auto标记(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{{{Role: schema.Assistant, Content: "好的"}}}}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_manual", ProjectID: 1, UserID: 1})
	a := &Agent{Planner: llm, Tools: newStubRegistryWith(), Repo: repo, Sink: &stubFrameSink{}}
	if err := a.Run(context.Background(), "thr_manual", "act_1", "开始", nil, skillForSnapshotTest()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	saved, _ := repo.ListMessages("thr_manual")
	var blocks []ContentBlock
	_ = json.Unmarshal(saved[0].ContentBlocks, &blocks)
	if blocks[0].Auto {
		t.Fatal("手动选的 skill block 不该带 auto")
	}
}
