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
