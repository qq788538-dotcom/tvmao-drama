package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

type stubBackboneRepo struct {
	active *BackboneModel
	err    error
}

func (s *stubBackboneRepo) Create(BackboneModel) (int64, error) { return 0, nil }
func (s *stubBackboneRepo) Update(BackboneModel) error          { return nil }
func (s *stubBackboneRepo) Delete(int64) error                  { return nil }
func (s *stubBackboneRepo) Get(int64) (*BackboneModel, error)   { return nil, nil }
func (s *stubBackboneRepo) List() ([]BackboneModel, error)      { return nil, nil }
func (s *stubBackboneRepo) GetActiveByRole(string) (*BackboneModel, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.active, nil
}

// stubSettings 模拟 settings repo：只剩 base_domain（上游 SK 改走 UpstreamKeyResolver）。
type stubSettings struct {
	base string
	err  error
}

func (s stubSettings) Get(k string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if k == "base_domain" {
		return s.base, nil
	}
	return "", nil
}

// stubKeyResolver 模拟 UpstreamKeyResolver：固定返回一把 key（空串 = 两边都没配）。
type stubKeyResolver struct{ key string }

func (s stubKeyResolver) Resolve(int64) (string, error) { return s.key, nil }

// stubModels 模拟 models repo：按 model_id 返回一个带计费配置的 ModelConfig。
// err 非空时直接报错（模拟匹配不到 → 拒绝）。
type stubModels struct {
	cfg *models.ModelConfig
	err error
}

func (s stubModels) GetByModelID(string) (*models.ModelConfig, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.cfg != nil {
		return s.cfg, nil
	}
	return &models.ModelConfig{ModelID: "x", BillingConfigRaw: json.RawMessage(`{"freezeEstimate":10}`)}, nil
}

// fullDeps 返回一个 PlannerDeps，接上给定的 backbone repo，并给 settings/models/
// upstream key 配上安全的默认值（base_domain + 可解析的 key + 命中计费配置）。
func fullDeps(repo BackboneRepo) PlannerDeps {
	return PlannerDeps{
		Backbones:   repo,
		Settings:    stubSettings{base: "https://newapi.example.com"},
		UpstreamKey: stubKeyResolver{key: "sk-global"},
		Models:      stubModels{},
	}
}

func TestBuildPlannerErrsWhenNoActive(t *testing.T) {
	repo := &stubBackboneRepo{err: ErrNoActiveBackbone}
	_, err := BuildPlannerForUser(context.Background(), 1, fullDeps(repo))
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

// Backbone 行走平台的 newapi upstream（base_domain + UpstreamKeyResolver 解析的 sk）。
// cfg.Provider 只是元数据——newapi 会归一成 OpenAI 形态——所以任何 provider 值都能
// 产出一个能用的 ChatModel。
func TestBuildPlanner_NewapiPath_UsesSettingsAndUserKey(t *testing.T) {
	repo := &stubBackboneRepo{active: &BackboneModel{
		Provider:  "openai",
		ModelName: "gpt-4o",
		MaxTokens: 4096,
	}}
	cm, err := BuildPlannerForUser(context.Background(), 1, fullDeps(repo))
	if err != nil {
		t.Fatalf("BuildPlannerForUser: %v", err)
	}
	if cm == nil {
		t.Fatal("nil ChatModel")
	}
}

func TestBuildPlanner_NewapiPath_IgnoresProviderValue(t *testing.T) {
	repo := &stubBackboneRepo{active: &BackboneModel{
		Provider:  "anthropic",
		ModelName: "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
	}}
	deps := PlannerDeps{
		Backbones:   repo,
		Settings:    stubSettings{base: "https://newapi.example.com/"},
		UpstreamKey: stubKeyResolver{key: "sk-global"},
		Models:      stubModels{},
	}
	cm, err := BuildPlannerForUser(context.Background(), 1, deps)
	if err != nil {
		t.Fatalf("BuildPlannerForUser: %v", err)
	}
	if cm == nil {
		t.Fatal("nil ChatModel")
	}
}

func TestBuildPlanner_NewapiPath_ErrsWhenNoBaseDomain(t *testing.T) {
	repo := &stubBackboneRepo{active: &BackboneModel{Provider: "openai"}}
	deps := PlannerDeps{
		Backbones:   repo,
		Settings:    stubSettings{base: ""},
		UpstreamKey: stubKeyResolver{key: "sk-global"},
		Models:      stubModels{},
	}
	if _, err := BuildPlannerForUser(context.Background(), 1, deps); err == nil {
		t.Fatal("expected error when base_domain empty")
	}
}

// resolver 解析出空串 = 用户没配 + 全局也没配 → 拒绝。
func TestBuildPlanner_NewapiPath_ErrsWhenNoUpstreamKey(t *testing.T) {
	repo := &stubBackboneRepo{active: &BackboneModel{Provider: "openai"}}
	deps := PlannerDeps{
		Backbones:   repo,
		Settings:    stubSettings{base: "https://x"},
		UpstreamKey: stubKeyResolver{key: ""},
		Models:      stubModels{},
	}
	if _, err := BuildPlannerForUser(context.Background(), 1, deps); err == nil {
		t.Fatal("expected error when resolved upstream key empty")
	}
}

// 匹配不到计费配置（model_configs 里查不到 backbone.ModelName）→ 拒绝 Run。
func TestBuildPlanner_ErrsWhenNoBillingConfig(t *testing.T) {
	repo := &stubBackboneRepo{active: &BackboneModel{Provider: "openai", ModelName: "ghost-model"}}
	deps := PlannerDeps{
		Backbones:   repo,
		Settings:    stubSettings{base: "https://x"},
		UpstreamKey: stubKeyResolver{key: "sk-global"},
		Models:      stubModels{err: models.ErrNotFound},
	}
	if _, err := BuildPlannerForUser(context.Background(), 1, deps); err == nil {
		t.Fatal("expected error when backbone model has no billing config")
	}
}

func TestNormaliseNewapiBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://newapi.example.com":     "https://newapi.example.com/v1",
		"https://newapi.example.com/":    "https://newapi.example.com/v1",
		"https://newapi.example.com/v1":  "https://newapi.example.com/v1",
		"https://newapi.example.com/v1/": "https://newapi.example.com/v1",
	}
	for in, want := range cases {
		if got := normaliseNewapiBaseURL(in); got != want {
			t.Errorf("normaliseNewapiBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}
