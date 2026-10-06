package http

import (
	"encoding/json"
	"fmt"
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
		req = httptest.NewRequest(method, "/api/v1/admin/canvas/gen-image-presets", nil)
	} else {
		req = httptest.NewRequest(method, "/api/v1/admin/canvas/gen-image-presets", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// 未配置/损坏 JSON 都要降级空配置——后台首次进入必须能渲染，不能 500。
func TestGenImagePresetAdminGetDegrades(t *testing.T) {
	h, store := genImagePresetHandler(t)
	for _, raw := range []string{"", "{broken"} {
		store.kv[canvasgen.SettingGenImagePresets] = raw
		rec := doGenImagePreset(t, h.AdminGet, http.MethodGet, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("raw=%q status=%d", raw, rec.Code)
		}
		var got canvasgen.GenImagePresetsConfig
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("raw=%q body=%s", raw, rec.Body.String())
		}
		if got.Models == nil || got.Presets == nil {
			t.Fatalf("raw=%q 应补齐空数组: %s", raw, rec.Body.String())
		}
	}
}

// PUT 的四类配置硬伤必须在落库前拦下：坏 key / 重 key / 缺 name/prompt / 图片数约束矛盾。
func TestGenImagePresetAdminPutValidation(t *testing.T) {
	h, _ := genImagePresetHandler(t)
	cases := []struct{ name, body string }{
		{"key 大写", `{"models":[],"presets":[{"key":"BadKey","value":{"name":"x","prompt":"p"}}]}`},
		{"key 为空", `{"models":[],"presets":[{"key":"","value":{"name":"x","prompt":"p"}}]}`},
		{"key 重复", `{"models":[],"presets":[{"key":"a","value":{"name":"x","prompt":"p"}},{"key":"a","value":{"name":"y","prompt":"q"}}]}`},
		{"缺 name", `{"models":[],"presets":[{"key":"a","value":{"prompt":"p"}}]}`},
		{"缺 prompt", `{"models":[],"presets":[{"key":"a","value":{"name":"x"}}]}`},
		{"value 非对象", `{"models":[],"presets":[{"key":"a","value":null}]}`},
		{"模型空串", `{"models":["  "],"presets":[]}`},
		{"minImages 负数", `{"models":[],"presets":[{"key":"a","value":{"name":"x","prompt":"p","minImages":-1}}]}`},
		{"minImages 小数", `{"models":[],"presets":[{"key":"a","value":{"name":"x","prompt":"p","minImages":1.5}}]}`},
		{"min 大于 max", `{"models":[],"presets":[{"key":"a","value":{"name":"x","prompt":"p","minImages":2,"maxImages":1}}]}`},
	}
	for _, c := range cases {
		if rec := doGenImagePreset(t, h.AdminPut, http.MethodPut, c.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s 应被拒，got %d %s", c.name, rec.Code, rec.Body.String())
		}
	}
}

// 合法配置落库后原样回读，value 里的未知字段（扩展位）必须原样保留。
func TestGenImagePresetAdminPutRoundTripKeepsUnknownFields(t *testing.T) {
	h, store := genImagePresetHandler(t)
	body := `{"models":["m1","m2"],"presets":[
		{"key":"three_view","value":{"name":"角色三视图","prompt":"白底三视图","category":"设定图","minImages":1,"maxImages":1,"futureParam":{"foo":1}}},
		{"key":"storyboard","value":{"name":"故事板","prompt":"分镜"}}]}`
	rec := doGenImagePreset(t, h.AdminPut, http.MethodPut, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
	cfg, err := canvasgen.LoadGenImagePresetsConfig(store.Get)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := cfg.Preset("three_view")
	if !ok || p.Name() != "角色三视图" || p.Prompt() != "白底三视图" {
		t.Fatalf("回读不符: %+v", p)
	}
	if _, ok := p.Value["futureParam"]; !ok {
		t.Fatalf("未知字段被吃掉: %+v", p.Value)
	}
	// 数组顺序保持（分类顺序依赖它）。
	if cfg.Presets[0].Key != "three_view" || cfg.Presets[1].Key != "storyboard" {
		t.Fatalf("顺序变了: %+v", cfg.Presets)
	}
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

// params 配置硬伤在 PUT 时拦下。
func TestGenImagePresetAdminPutValidatesParams(t *testing.T) {
	h, _ := genImagePresetHandler(t)
	base := `{"models":[],"presets":[{"key":"a","value":{"name":"x","prompt":"p","params":%s}}]}`
	cases := []struct{ name, params string }{
		{"不是数组", `"oops"`},
		{"维度缺 key", `[{"name":"皮肤","options":[{"key":"a","name":"甲"}]}]`},
		{"维度 key 重复", `[{"key":"d","name":"一","options":[{"key":"a","name":"甲"}]},{"key":"d","name":"二","options":[{"key":"a","name":"甲"}]}]`},
		{"维度无档位", `[{"key":"d","name":"一","options":[]}]`},
		{"档位缺 name", `[{"key":"d","name":"一","options":[{"key":"a"}]}]`},
		{"档位 key 重复", `[{"key":"d","name":"一","options":[{"key":"a","name":"甲"},{"key":"a","name":"乙"}]}]`},
	}
	for _, c := range cases {
		if rec := doGenImagePreset(t, h.AdminPut, http.MethodPut, fmt.Sprintf(base, c.params)); rec.Code != http.StatusBadRequest {
			t.Errorf("%s 应被拒，got %d %s", c.name, rec.Code, rec.Body.String())
		}
	}
	// 合法配置（档位 prompt 允许为空 = 无效果档位）。
	ok := `[{"key":"d","name":"一","options":[{"key":"a","name":"甲","prompt":""},{"key":"b","name":"乙","prompt":"x","default":true}]}]`
	if rec := doGenImagePreset(t, h.AdminPut, http.MethodPut, fmt.Sprintf(base, ok)); rec.Code != http.StatusOK {
		t.Fatalf("合法 params 被拒: %d %s", rec.Code, rec.Body.String())
	}
}
