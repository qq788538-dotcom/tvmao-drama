package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasgen"
)

func lightControlHandler(t *testing.T) (*LightControlHandler, *presetMemSettings) {
	t.Helper()
	st := newPresetMemSettings()
	return NewLightControlHandler(st), st
}

func doLightControl(t *testing.T, h http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api/v1/canvas/light-control", nil)
	} else {
		req = httptest.NewRequest(method, "/api/v1/canvas/light-control", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

const lightControlFixture = `{
  "models": ["model-a", "model-b"],
  "template": "光影：{light}",
  "presets": [
    {"key": "rembrandt", "name": "伦勃朗光", "direction": "high-front-left", "prompt": "机密附加词"},
    {"key": "overexposed", "name": "过曝胶片", "brightness": 100, "thumbnail": "https://cdn/x.webp", "prompt": ""},
    {"key": "sunset", "name": "落日迷幻", "referenceImage": "https://cdn/ref.jpg"}
  ]
}`

// UserGet 脱敏铁律：prompt 与 template 绝不下发；参数字段与参考图要在(表单跳位用)。
func TestLightControlUserGetStripsSecrets(t *testing.T) {
	h, store := lightControlHandler(t)
	store.kv[canvasgen.SettingCanvasLightControl] = lightControlFixture

	rec := doLightControl(t, h.UserGet, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	for _, secret := range []string{"机密附加词", "prompt", "template", "光影：{light}"} {
		if strings.Contains(body, secret) {
			t.Fatalf("泄露保密字段 %q: %s", secret, body)
		}
	}
	var got struct {
		Models  []string `json:"models"`
		Presets []struct {
			Key            string `json:"key"`
			Name           string `json:"name"`
			Thumbnail      string `json:"thumbnail"`
			Direction      string `json:"direction"`
			Brightness     *int   `json:"brightness"`
			ReferenceImage string `json:"referenceImage"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 || len(got.Presets) != 3 {
		t.Fatalf("形状不对: %s", body)
	}
	if got.Presets[0].Direction != "high-front-left" {
		t.Fatalf("参数字段应下发: %+v", got.Presets[0])
	}
	if got.Presets[1].Brightness == nil || *got.Presets[1].Brightness != 100 {
		t.Fatalf("brightness 应下发: %+v", got.Presets[1])
	}
	// 未设置 brightness 的条目不能出 null——前端只判「键在不在」一种空。
	if got.Presets[0].Brightness != nil {
		t.Fatalf("未设置的 brightness 不该下发: %+v", got.Presets[0])
	}
	if got.Presets[2].ReferenceImage != "https://cdn/ref.jpg" {
		t.Fatalf("参考图 URL 应下发: %+v", got.Presets[2])
	}
}
