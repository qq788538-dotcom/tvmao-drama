// apps/api/internal/agent/registry_db_test.go
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestDBRegistry_LoadsFromRepo(t *testing.T) {
	repo := &stubAgentToolsRepo{rows: []AgentToolRow{
		{
			ID: 1, ToolName: "gen_test", Category: "image",
			SourceModelID: 42, ToolDescription: "test gen",
			ParamsSchema:  json.RawMessage(`{"prompt":{"type":"string","required":true}}`),
			DefaultParams: json.RawMessage(`{}`),
			IsPublished:   true,
		},
	}}
	resolver := &stubModelResolver{slug: "test-model-id"}
	reg := NewDBRegistry(repo, resolver, &stubGenSvc{}, PlannerDeps{}, nil, nil, nil, nil, nil, nil, "")
	if err := reg.RefreshFromDB(); err != nil {
		t.Fatal(err)
	}

	tools := reg.Snapshot()
	// DBRegistry 总会注入内置工具（meta + analyze_image + analyze_video）——
	// 在断言用户工具数量前把它们过滤掉。
	builtins := map[string]bool{
		AnalyzeImageToolName:       true,
		AnalyzeVideoToolName:       true,
		AnalyzeAudioToolName:       true,
		CreateTextNodeToolName:     true,
		LoadSkillToolName:          true,
		CreateGenNodeToolName:      true,
		ReadCanvasToolName:         true,
		CreateNodeToolName:         true,
		UpdateNodeToolName:         true,
		DeleteNodeToolName:         true,
		ConnectNodesToolName:       true,
		RemoveConnectionToolName:   true,
		ListCanvasModelsToolName:   true,
		DescribeCanvasNodeToolName: true,
		ReadAttachmentToolName:     true,
	}
	var userTools []Tool
	for _, tl := range tools {
		info, _ := tl.Info(context.Background())
		if !builtins[info.Name] {
			userTools = append(userTools, tl)
		}
	}
	if len(userTools) != 1 {
		t.Fatalf("want 1 user tool, got %d", len(userTools))
	}
	info, _ := userTools[0].Info(context.Background())
	if info.Name != "gen_test" {
		t.Fatalf("name: %s", info.Name)
	}
	if _, ok := reg.ByName("gen_test"); !ok {
		t.Fatal("ByName missed")
	}
	if _, ok := reg.ByName(OutputSkillDefinitionToolName); ok {
		t.Fatal("output_skill_definition 不应出现在工具目录")
	}
}

func TestDBRegistry_RefreshUpdates(t *testing.T) {
	repo := &stubAgentToolsRepo{rows: []AgentToolRow{
		{ID: 1, ToolName: "tool_a", Category: "image", SourceModelID: 1, ParamsSchema: json.RawMessage(`{}`), DefaultParams: json.RawMessage(`{}`), IsPublished: true},
	}}
	resolver := &stubModelResolver{}
	reg := NewDBRegistry(repo, resolver, &stubGenSvc{}, PlannerDeps{}, nil, nil, nil, nil, nil, nil, "")
	reg.RefreshFromDB()

	// admin 改了工具集
	repo.rows = []AgentToolRow{
		{ID: 2, ToolName: "tool_b", Category: "image", SourceModelID: 1, ParamsSchema: json.RawMessage(`{}`), DefaultParams: json.RawMessage(`{}`), IsPublished: true},
	}
	reg.RefreshFromDB()

	if _, ok := reg.ByName("tool_a"); ok {
		t.Fatal("tool_a should be gone after refresh")
	}
	if _, ok := reg.ByName("tool_b"); !ok {
		t.Fatal("tool_b should be present")
	}
}

// 慢视频 model（grok 等）的真实形态：后端为它的任务等 20 分钟，远长于工具硬编码
// 的 5 分钟 defaultMaxWait。工具的轮询上限必须跟着 model 预算走，否则它先超时、
// planner 反复重试、对话一直转圈，而后端照样把视频跑完进历史。
func TestDBRegistry_AlignsMaxWaitToModelBudget(t *testing.T) {
	repo := &stubAgentToolsRepo{rows: []AgentToolRow{
		{ID: 1, ToolName: "gen_video", Category: "video", SourceModelID: 7,
			ParamsSchema: json.RawMessage(`{}`), DefaultParams: json.RawMessage(`{}`), IsPublished: true},
	}}
	resolver := &stubModelResolver{slug: "grok-video", maxWait: 20 * time.Minute}
	reg := NewDBRegistry(repo, resolver, &stubGenSvc{}, PlannerDeps{}, nil, nil, nil, nil, nil, nil, "")
	if err := reg.RefreshFromDB(); err != nil {
		t.Fatal(err)
	}
	tl, ok := reg.ByName("gen_video")
	if !ok {
		t.Fatal("tool missing")
	}
	dt, ok := tl.(*DynamicTool)
	if !ok {
		t.Fatalf("want *DynamicTool, got %T", tl)
	}
	// 工具上限 = model 预算 + 30s 余量。
	if want := 20*time.Minute + 30*time.Second; dt.MaxWait != want {
		t.Fatalf("MaxWait: got %s, want %s", dt.MaxWait, want)
	}
}

// resolver 拿不到预算（出错 / model 没配）时，工具 MaxWait 必须留 0，让
// pollUntilDone 回退到 defaultMaxWait——绝不能反而把上限调成一个更小的值。
func TestDBRegistry_MaxWaitFallsBackWhenBudgetUnknown(t *testing.T) {
	repo := &stubAgentToolsRepo{rows: []AgentToolRow{
		{ID: 1, ToolName: "gen_img", Category: "image", SourceModelID: 1,
			ParamsSchema: json.RawMessage(`{}`), DefaultParams: json.RawMessage(`{}`), IsPublished: true},
	}}
	resolver := &stubModelResolver{slug: "img", budgetErr: true}
	reg := NewDBRegistry(repo, resolver, &stubGenSvc{}, PlannerDeps{}, nil, nil, nil, nil, nil, nil, "")
	if err := reg.RefreshFromDB(); err != nil {
		t.Fatal(err)
	}
	tl, _ := reg.ByName("gen_img")
	dt := tl.(*DynamicTool)
	if dt.MaxWait != 0 {
		t.Fatalf("MaxWait should be 0 (default fallback), got %s", dt.MaxWait)
	}
}

type stubAgentToolsRepo struct{ rows []AgentToolRow }

func (s *stubAgentToolsRepo) Create(t AgentToolRow) (int64, error) { return 0, nil }
func (s *stubAgentToolsRepo) Update(t AgentToolRow) error          { return nil }
func (s *stubAgentToolsRepo) Delete(id int64) error                { return nil }
func (s *stubAgentToolsRepo) Get(id int64) (*AgentToolRow, error)  { return nil, nil }
func (s *stubAgentToolsRepo) List() ([]AgentToolRow, error)        { return s.rows, nil }
func (s *stubAgentToolsRepo) ListPublished() ([]AgentToolRow, error) {
	out := []AgentToolRow{}
	for _, r := range s.rows {
		if r.IsPublished {
			out = append(out, r)
		}
	}
	return out, nil
}
func (s *stubAgentToolsRepo) MaxUpdatedAt() (time.Time, error) { return time.Now(), nil }

type stubModelResolver struct {
	slug      string
	maxWait   time.Duration
	budgetErr bool
}

func (s *stubModelResolver) ResolveModelSlug(id int64) (string, error) {
	if s.slug == "" {
		return "stub-model", nil
	}
	return s.slug, nil
}

func (s *stubModelResolver) ResolvePollMaxWait(id int64) (time.Duration, error) {
	if s.budgetErr {
		return 0, errors.New("no budget")
	}
	return s.maxWait, nil
}

// DisplayName 的解析次序：agent_tools.display_name（后台热配置）优先，内置工具
// 落到代码里的中文名表，两边都查不到返回空串（调用方回退内部名）。
func TestDBRegistry_DisplayName(t *testing.T) {
	repo := &stubAgentToolsRepo{rows: []AgentToolRow{
		{ID: 1, ToolName: "gen_image", DisplayName: "生成图片", Category: "image", SourceModelID: 1, ParamsSchema: json.RawMessage(`{}`), DefaultParams: json.RawMessage(`{}`), IsPublished: true},
		{ID: 2, ToolName: "gen_video", DisplayName: "", Category: "video", SourceModelID: 1, ParamsSchema: json.RawMessage(`{}`), DefaultParams: json.RawMessage(`{}`), IsPublished: true},
	}}
	reg := NewDBRegistry(repo, &stubModelResolver{}, &stubGenSvc{}, PlannerDeps{}, nil, nil, nil, nil, nil, nil, "")
	if err := reg.RefreshFromDB(); err != nil {
		t.Fatal(err)
	}

	if got := reg.DisplayName("gen_image"); got != "生成图片" {
		t.Fatalf("db display name: %q", got)
	}
	// display_name 留空的 DB 工具不该报出空串以外的东西——尤其不能错报别的工具的名字。
	if got := reg.DisplayName("gen_video"); got != "" {
		t.Fatalf("empty db display name should stay empty, got %q", got)
	}
	if got := reg.DisplayName(ReadCanvasToolName); got != "读取画布" {
		t.Fatalf("builtin display name: %q", got)
	}
	if got := reg.DisplayName("no_such_tool"); got != "" {
		t.Fatalf("unknown tool should return empty, got %q", got)
	}
}
