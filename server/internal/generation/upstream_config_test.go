package generation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

// 每模型独立上游：执行计划里的地址与 Key 经 models.ResolveUpstream 解析。
func TestPrepareExecutionUsesModelOwnUpstream(t *testing.T) {
	m := &models.ModelConfig{ModelID: "m", Enabled: true, RequestTemplate: json.RawMessage(`{"prompt":"{{prompt}}"}`),
		UpstreamBaseURL: "https://own.example", UpstreamAPIKey: "sk-own"}
	plan, err := planService(m, okSettings(), "sk-global").prepareExecution(context.Background(), 1, "m", map[string]any{"prompt": "p"}, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.baseDomain != "https://own.example" || plan.apiKey != "sk-own" {
		t.Fatalf("应走模型自己的上游, got %q %q", plan.baseDomain, plan.apiKey)
	}
}

func TestPrepareExecutionFallsBackToGlobalUpstream(t *testing.T) {
	m := &models.ModelConfig{ModelID: "m", Enabled: true, RequestTemplate: json.RawMessage(`{"prompt":"{{prompt}}"}`)}
	plan, err := planService(m, okSettings(), "sk-global").prepareExecution(context.Background(), 1, "m", map[string]any{"prompt": "p"}, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.baseDomain != "https://api.example.com" || plan.apiKey != "sk-global" {
		t.Fatalf("应走全局上游, got %q %q", plan.baseDomain, plan.apiKey)
	}
}

// 全局没配，但模型自己配全了：照样能执行（不再被全局缺失挡住）。
func TestUpstreamCredentialsModelOwnWithoutGlobal(t *testing.T) {
	s := planService(nil, planSettings{}, "")
	key, base, err := s.upstreamCredentials(1, &models.ModelConfig{UpstreamBaseURL: "https://own.example", UpstreamAPIKey: "sk-own"})
	if err != nil || key != "sk-own" || base != "https://own.example" {
		t.Fatalf("got %q %q %v", key, base, err)
	}
	if _, _, err := s.upstreamCredentials(1, &models.ModelConfig{}); !errors.Is(err, ErrUpstreamKeyNotConfigured) {
		t.Fatalf("未配置时应报 Key 未配置, got %v", err)
	}
	s = planService(nil, planSettings{}, "sk-global")
	if _, _, err := s.upstreamCredentials(1, &models.ModelConfig{}); !errors.Is(err, ErrBaseDomainNotConfigured) {
		t.Fatalf("未配置地址时应报地址未配置, got %v", err)
	}
}

// 画布生成拼 baseDomain + base_path（种子 base_path 自带 /v1）。独立上游存成 .../v1
// 的旧值时，执行计划里的 baseDomain 已去掉 /v1，最终 URL 只有一层 /v1。
func TestPrepareExecutionStripsTrailingV1(t *testing.T) {
	m := &models.ModelConfig{ModelID: "m", Enabled: true, BasePath: "/v1/chat/completions", RequestTemplate: json.RawMessage(`{"prompt":"{{prompt}}"}`),
		UpstreamBaseURL: "https://own.example/v1/", UpstreamAPIKey: "sk-own"}
	plan, err := planService(m, okSettings(), "sk-global").prepareExecution(context.Background(), 1, "m", map[string]any{"prompt": "p"}, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.baseDomain + m.BasePath; got != "https://own.example/v1/chat/completions" {
		t.Fatalf("got %q", got)
	}
}
