package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
)

// presetMemSettings 定义在 preset_testhelpers_test.go。

func genImagePresetHandler(t *testing.T) (*GenImagePresetHandler, *presetMemSettings) {
	t.Helper()
	st := newPresetMemSettings()
	return NewGenImagePresetHandler(st), st
}

func doGenImagePreset(t *testing.T, h http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api/v1/canvas/gen-image-presets", nil)
	} else {
		req = httptest.NewRequest(method, "/api/v1/canvas/gen-image-presets", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// 用户端点是保密防线：响应任何角落都不许出现 prompt 正文；白名单字段照常下发。
func TestGenImagePresetUserGetStripsPrompt(t *testing.T) {
	h, store := genImagePresetHandler(t)
	store.kv[canvasgen.SettingGenImagePresets] = `{"models":["m1"],"presets":[
		{"key":"three_view","value":{"name":"角色三视图","prompt":"这段正文绝不能泄露","category":"设定图","placeholder":"点击生成","minImages":1,"secretFuture":"也不该下发"}}]}`
	rec := doGenImagePreset(t, h.UserGet, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "这段正文绝不能泄露") || strings.Contains(body, "prompt") {
		t.Fatalf("prompt 泄露: %s", body)
	}
	// 白名单外的未知字段也不下发（白名单语义：没列的都算潜在敏感）。
	if strings.Contains(body, "secretFuture") {
		t.Fatalf("白名单外字段泄露: %s", body)
	}
	var got struct {
		Models  []string `json:"models"`
		Presets []struct {
			Key   string         `json:"key"`
			Value map[string]any `json:"value"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || len(got.Presets) != 1 {
		t.Fatalf("结构不符: %s", body)
	}
	v := got.Presets[0].Value
	if v["name"] != "角色三视图" || v["category"] != "设定图" || v["placeholder"] != "点击生成" || v["minImages"] != float64(1) {
		t.Fatalf("白名单字段缺失: %v", v)
	}
}

// ImageBounds 的取值语义：缺省=未配置；float 整数值正常转换。
func TestGenImagePresetImageBounds(t *testing.T) {
	var e canvasgen.GenImagePresetEntry
	_ = json.Unmarshal([]byte(`{"key":"a","value":{"name":"x","prompt":"p","minImages":1}}`), &e)
	min, hasMin, _, hasMax := e.ImageBounds()
	if !hasMin || min != 1 || hasMax {
		t.Fatalf("min=%d hasMin=%v hasMax=%v", min, hasMin, hasMax)
	}
}

// 带 params 的预设：用户端点下发维度/档位名字（渲染面板要用），但每个档位的
// prompt 片段与预设正文同级保密——嵌套剥离是这个功能里最容易漏的泄露面。
func TestGenImagePresetUserGetStripsParamPrompts(t *testing.T) {
	h, store := genImagePresetHandler(t)
	store.kv[canvasgen.SettingGenImagePresets] = `{"models":["m1"],"presets":[
		{"key":"portrait","value":{"name":"人像质感调节","prompt":"主正文密文",
			"params":[{"key":"skin","name":"皮肤","options":[
				{"key":"natural","name":"自然肤质","prompt":"档位片段密文","default":true},
				{"key":"real","name":"真实肌理","prompt":"另一段密文"}]}]}}]}`
	rec := doGenImagePreset(t, h.UserGet, http.MethodGet, "")
	body := rec.Body.String()
	for _, secret := range []string{"主正文密文", "档位片段密文", "另一段密文", "prompt"} {
		if strings.Contains(body, secret) {
			t.Fatalf("泄露 %q: %s", secret, body)
		}
	}
	// 展示字段完整：维度名/档位名/default 都在。
	for _, want := range []string{"皮肤", "自然肤质", "真实肌理", `"default":true`, `"key":"skin"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("缺展示字段 %q: %s", want, body)
		}
	}
}
