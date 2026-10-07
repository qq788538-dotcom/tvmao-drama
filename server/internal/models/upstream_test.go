package models

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type mapSettings map[string]string

func (m mapSettings) Get(k string) (string, error) { return m[k], nil }

type errSettings struct{}

func (errSettings) Get(string) (string, error) { return "", errors.New("boom") }

func TestResolveUpstreamPrefersModelWhenBothSet(t *testing.T) {
	g := mapSettings{"base_domain": "http://global", "upstream_api_key": "sk-global"}
	m := &ModelConfig{UpstreamBaseURL: "https://own.example", UpstreamAPIKey: "sk-own"}
	base, key, err := ResolveUpstream(m, g)
	if err != nil || base != "https://own.example" || key != "sk-own" {
		t.Fatalf("got %q %q %v", base, key, err)
	}
}

func TestResolveUpstreamFallsBackToGlobal(t *testing.T) {
	g := mapSettings{"base_domain": "http://global", "upstream_api_key": "sk-global"}
	for name, m := range map[string]*ModelConfig{
		"nil model":   nil,
		"未配置":         {},
		"只有地址（坏数据）":   {UpstreamBaseURL: "https://own.example"},
		"只有 Key（坏数据）": {UpstreamAPIKey: "sk-own"},
		"空白":          {UpstreamBaseURL: "  ", UpstreamAPIKey: " "},
	} {
		base, key, err := ResolveUpstream(m, g)
		if err != nil || base != "http://global" || key != "sk-global" {
			t.Errorf("%s: got %q %q %v", name, base, key, err)
		}
	}
}

func TestResolveUpstreamGlobalReadError(t *testing.T) {
	if _, _, err := ResolveUpstream(&ModelConfig{}, errSettings{}); err == nil {
		t.Fatal("全局读取失败应返回错误")
	}
	// 模型自己配全了就不需要读全局。
	if _, _, err := ResolveUpstream(&ModelConfig{UpstreamBaseURL: "https://a", UpstreamAPIKey: "k"}, errSettings{}); err != nil {
		t.Fatal(err)
	}
}

func TestGlobalWithKeyOverridesOnlyKey(t *testing.T) {
	g := GlobalWithKey(mapSettings{"base_domain": "http://global", "upstream_api_key": "sk-settings"}, func() (string, error) { return "sk-user", nil })
	base, key, err := ResolveUpstream(nil, g)
	if err != nil || base != "http://global" || key != "sk-user" {
		t.Fatalf("got %q %q %v", base, key, err)
	}
}

func TestSetUpstreamRoundTripAndHiddenFromJSON(t *testing.T) {
	conn := openTestDB(t)
	repo := NewSQLiteRepository(conn)
	created, err := repo.Create(ModelConfig{Type: "image", Name: "m", ModelID: "up-m", BasePath: "/v1/x", Enabled: true, RequestTemplate: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetUpstream(created.ID, "https://own.example", "sk-secret-123456"); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByModelID("up-m")
	if err != nil || got.UpstreamBaseURL != "https://own.example" || got.UpstreamAPIKey != "sk-secret-123456" {
		t.Fatalf("GetByModelID: %+v %v", got, err)
	}
	byID, _ := repo.GetByID(created.ID)
	list, _ := repo.List(false)
	if byID.UpstreamAPIKey != "sk-secret-123456" || len(list) != 1 || list[0].UpstreamAPIKey != "sk-secret-123456" {
		t.Fatal("GetByID / List 也应读出独立上游")
	}
	// ModelConfig 会被多个接口直接序列化（/api/v1/models 等），Key 明文绝不能出现在 JSON 里。
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "sk-secret") || strings.Contains(string(b), "own.example") {
		t.Fatalf("ModelConfig JSON 泄露了独立上游: %s", b)
	}
	// 普通 Update 不得清掉独立上游。
	got.Name = "改名"
	if _, err := repo.Update(got.ID, *got); err != nil {
		t.Fatal(err)
	}
	if again, _ := repo.GetByID(created.ID); again.UpstreamAPIKey != "sk-secret-123456" {
		t.Fatal("Update 清掉了独立上游")
	}
	if err := repo.SetUpstream(created.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if again, _ := repo.GetByID(created.ID); again.UpstreamBaseURL != "" || again.UpstreamAPIKey != "" {
		t.Fatal("清空失败")
	}
	if err := repo.SetUpstream(created.ID, "https://a", ""); err == nil {
		t.Fatal("只给一项应被拒绝")
	}
	if err := repo.SetUpstream(99999, "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的模型应返回 ErrNotFound, got %v", err)
	}
}
