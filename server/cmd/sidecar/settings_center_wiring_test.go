package main

import (
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/builtin"
)

// P4 T15：设置接口由真实装配接上 builtin.Service；/admin/canvas/* 的整份覆盖写接口不存在，
// 画布配置只能经 /api/v1/settings/configs/* 改（白名单强制）。
func TestE2ESettingsCenterWiredAndAdminCanvasAbsent(t *testing.T) {
	e := newE2E(t)
	if err := builtin.Apply(e.conn); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/v1/settings/general", "/api/v1/settings/models", "/api/v1/settings/configs/canvas_multi_angle", "/api/v1/settings/agent-models"} {
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
	// 白名单外的改动经真实装配同样被拒。
	resp, body := e.callJSON(t, "PUT", "/api/v1/settings/configs/canvas_script_config", `{"value":{"styles":[]}}`)
	if resp.StatusCode != 400 || !strings.Contains(string(body), "error") {
		t.Fatalf("应 400：%d %s", resp.StatusCode, body)
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
