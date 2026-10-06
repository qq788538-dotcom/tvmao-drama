package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
)

func multiAngleHandler(t *testing.T) (*MultiAngleHandler, *presetMemSettings) {
	t.Helper()
	st := newPresetMemSettings()
	return NewMultiAngleHandler(st), st
}

func doMultiAngle(t *testing.T, h http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api/v1/canvas/multi-angle", nil)
	} else {
		req = httptest.NewRequest(method, "/api/v1/canvas/multi-angle", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

const multiAngleFixture = `{
  "models": ["model-a", "model-b"],
  "template": "视角：{angle}",
  "presets": [
    {"key": "fisheye", "name": "鱼眼视角", "rotation": 0, "tilt": 30, "zoom": "closeup", "prompt": "机密附加词"},
    {"key": "back", "name": "背面视角", "rotation": 180, "tilt": 0, "zoom": "medium", "prompt": ""}
  ]
}`

// UserGet 脱敏铁律：prompt 与 template 绝不下发；角度数值与名字要在(滑杆跳位用)。
func TestMultiAngleUserGetStripsSecrets(t *testing.T) {
	h, store := multiAngleHandler(t)
	store.kv[canvasgen.SettingCanvasMultiAngle] = multiAngleFixture

	rec := doMultiAngle(t, h.UserGet, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	for _, secret := range []string{"机密附加词", "prompt", "template", "视角：{angle}"} {
		if strings.Contains(body, secret) {
			t.Fatalf("泄露保密字段 %q: %s", secret, body)
		}
	}
	var got struct {
		Models  []string `json:"models"`
		Presets []struct {
			Key      string `json:"key"`
			Name     string `json:"name"`
			Rotation int    `json:"rotation"`
			Tilt     int    `json:"tilt"`
			Zoom     string `json:"zoom"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 || len(got.Presets) != 2 {
		t.Fatalf("形状不对: %s", body)
	}
	if got.Presets[0].Key != "fisheye" || got.Presets[0].Tilt != 30 || got.Presets[0].Zoom != "closeup" {
		t.Fatalf("角度数值应下发: %+v", got.Presets[0])
	}
}
