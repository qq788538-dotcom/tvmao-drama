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
		req = httptest.NewRequest(method, "/api/v1/admin/canvas/multi-angle", nil)
	} else {
		req = httptest.NewRequest(method, "/api/v1/admin/canvas/multi-angle", strings.NewReader(body))
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

func TestMultiAngleAdminPutValidation(t *testing.T) {
	h, _ := multiAngleHandler(t)
	for name, body := range map[string]string{
		"key保留字":   `{"models":[],"presets":[{"key":"__multi_angle__","name":"x","rotation":0,"tilt":0,"zoom":"medium"}]}`,
		"key重复":    `{"models":[],"presets":[{"key":"a","name":"x","rotation":0,"tilt":0,"zoom":"medium"},{"key":"a","name":"y","rotation":0,"tilt":0,"zoom":"medium"}]}`,
		"zoom非法":   `{"models":[],"presets":[{"key":"a","name":"x","rotation":0,"tilt":0,"zoom":"huge"}]}`,
		"rotation": `{"models":[],"presets":[{"key":"a","name":"x","rotation":360,"tilt":0,"zoom":"medium"}]}`,
		"tilt越界":   `{"models":[],"presets":[{"key":"a","name":"x","rotation":0,"tilt":91,"zoom":"medium"}]}`,
		"name为空":   `{"models":[],"presets":[{"key":"a","name":" ","rotation":0,"tilt":0,"zoom":"medium"}]}`,
		"空模型ID":    `{"models":[" "],"presets":[]}`,
	} {
		if rec := doMultiAngle(t, h.AdminPut, http.MethodPut, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: 期望 400, got %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// 合法配置(含空 prompt 的纯角度预设)要能写入并回读。
	rec := doMultiAngle(t, h.AdminPut, http.MethodPut, multiAngleFixture)
	if rec.Code != http.StatusOK {
		t.Fatalf("合法配置被拒: %d %s", rec.Code, rec.Body.String())
	}
	rec = doMultiAngle(t, h.AdminGet, http.MethodGet, "")
	if !strings.Contains(rec.Body.String(), "机密附加词") {
		t.Fatalf("admin 回显应含 prompt: %s", rec.Body.String())
	}
}
