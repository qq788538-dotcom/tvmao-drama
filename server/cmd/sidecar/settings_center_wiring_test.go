package main

import (
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/builtin"
)

// P4 T15：设置接口由真实装配接上 builtin.Service；/admin/canvas/* 的整份覆盖写接口不存在。
// 2026-10-07 起五项画布配置与 Agent 模型写死：/api/v1/settings/configs/*、/agent-models 也已注销。
func TestE2ESettingsCenterWiredAndAdminCanvasAbsent(t *testing.T) {
	e := newE2E(t)
	if err := builtin.Apply(e.conn); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/v1/settings/general", "/api/v1/settings/models"} {
		if resp, body := e.callJSON(t, "GET", p, ""); resp.StatusCode != 200 {
			t.Errorf("GET %s → %d %s", p, resp.StatusCode, body)
		}
	}
	for _, p := range []string{"multi-angle", "light-control", "gen-image-presets", "postprocess-config"} {
		for _, m := range []string{"GET", "PUT"} {
			resp, body := e.callJSON(t, m, "/api/v1/admin/canvas/"+p, `{"presets":[]}`)
			if resp.StatusCode != 404 && resp.StatusCode != 405 {
				t.Errorf("%s /admin/canvas/%s 不应注册：%d %s", m, p, resp.StatusCode, body)
			}
		}
	}
	// 写死的配置经真实装配也写不进去。
	before := map[string]string{}
	for _, k := range []string{"canvas_script_config", "canvas_multi_angle"} {
		var v string
		if err := e.conn.QueryRow(`SELECT value FROM settings WHERE "key" = ?`, k).Scan(&v); err != nil {
			t.Fatal(err)
		}
		before[k] = v
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/v1/settings/configs/canvas_multi_angle", ""},
		{"PUT", "/api/v1/settings/configs/canvas_script_config", `{"value":{"styles":[]}}`},
		{"POST", "/api/v1/settings/configs/canvas_multi_angle/restore-default", ""},
		{"GET", "/api/v1/settings/agent-models", ""},
		{"PUT", "/api/v1/settings/agent-models/1", `{"modelName":"gpt-5.5"}`},
		{"POST", "/api/v1/settings/agent-models/1/restore-default", ""},
	} {
		resp, body := e.callJSON(t, c.method, c.path, c.body)
		if resp.StatusCode != 404 && resp.StatusCode != 405 {
			t.Errorf("%s %s 应 404/405：%d %s", c.method, c.path, resp.StatusCode, body)
		}
	}
	for k, v := range before {
		var now string
		_ = e.conn.QueryRow(`SELECT value FROM settings WHERE "key" = ?`, k).Scan(&now)
		if now != v {
			t.Errorf("%s 被改动", k)
		}
	}
}

// 每模型独立上游经真实装配可写；任何接口响应都不带 Key 明文；内置字段编辑 / 恢复默认不影响它。
func TestE2EModelUpstreamWiredAndNeverLeaksKey(t *testing.T) {
	e := newE2E(t)
	if err := builtin.Apply(e.conn); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := e.conn.QueryRow(`SELECT id FROM model_configs ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	const key = "sk-e2e-secret-key-9876"
	path := "/api/v1/settings/models/" + itoa(id)
	resp, body := e.callJSON(t, "PATCH", path, `{"name":"我的模型","upstream":{"baseUrl":"https://own.example","apiKey":"`+key+`"}}`)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"configured":true`) {
		t.Fatalf("PATCH: %d %s", resp.StatusCode, body)
	}
	if resp, body = e.callJSON(t, "POST", path+"/restore-default", ""); resp.StatusCode != 200 {
		t.Fatalf("restore: %d %s", resp.StatusCode, body)
	}
	var base, k string
	if err := e.conn.QueryRow(`SELECT upstream_base_url, upstream_api_key FROM model_configs WHERE id = ?`, id).Scan(&base, &k); err != nil || base != "https://own.example" || k != key {
		t.Fatalf("恢复默认不应清掉独立上游: %q %q %v", base, k, err)
	}
	for _, p := range []string{"/api/v1/settings/models", "/api/v1/models"} {
		resp, body := e.callJSON(t, "GET", p, "")
		if resp.StatusCode != 200 || strings.Contains(string(body), key) || strings.Contains(string(body), "secret") {
			t.Errorf("GET %s → %d，不得含 Key 明文: %s", p, resp.StatusCode, body)
		}
	}
}
