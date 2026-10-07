package http

// 审查 I7：画布配置的白名单在适配器层强制执行。用真实库 + 真实内置种子 + 真实 builtin.Service，
// 经设置页的 HTTP 接口提交，断言白名单以外的改动一律 400、且库里的值和“已修改”记录都不变。

import (
	"bytes"
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/agent"
	"github.com/james/huabu_studio/apps/api/internal/agent/agentseed"
	"github.com/james/huabu_studio/apps/api/internal/builtin"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/settings"
)

type wlEnv struct {
	t        *testing.T
	h        nethttp.Handler
	settings settings.Repository
	policy   *builtin.Service
}

func newWhitelistEnv(t *testing.T) *wlEnv {
	t.Helper()
	conn := dbtest.Open(t)
	if err := builtin.Apply(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := agentseed.Import(conn); err != nil {
		t.Fatal(err)
	}
	policy, err := builtin.NewService(conn)
	if err != nil {
		t.Fatal(err)
	}
	sr, mr := settings.NewSQLiteRepository(conn), models.NewSQLiteRepository(conn)
	r := chi.NewRouter()
	mountSettingsCenter(r, SettingsCenterDeps{
		Settings:    sr,
		Models:      mr,
		ModelEdit:   NewPolicyModelEdit(policy, mr),
		SettingEdit: NewPolicySettingEdit(policy, sr),
		Agent:       NewBackboneAgentModels(agent.NewBackboneRepo(conn), agent.NewBackboneSwitcher(conn), agentseed.DefaultBackboneModelName),
		DataDir:     t.TempDir(),
		Version:     "test",
	})
	return &wlEnv{t: t, h: r, settings: sr, policy: policy}
}

func (e *wlEnv) do(method, path, body string) (int, string) {
	e.t.Helper()
	req := httptest.NewRequest(method, "/api/v1/settings"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func (e *wlEnv) raw(key string) string {
	e.t.Helper()
	v, err := e.settings.Get(key)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *wlEnv) modified(key string) bool {
	e.t.Helper()
	states, err := e.policy.ListSettings(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	for _, st := range states {
		if st.Key == key {
			return len(st.Overrides) > 0
		}
	}
	return false
}

// mutate 读出当前值，交给 f 改，返回 PUT 用的请求体。
func (e *wlEnv) mutate(key string, f func(doc map[string]any)) string {
	e.t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(e.raw(key)), &doc); err != nil {
		e.t.Fatal(err)
	}
	f(doc)
	b, _ := json.Marshal(map[string]any{"value": doc})
	return string(b)
}

func presetsOf(doc map[string]any) []any { a, _ := doc["presets"].([]any); return a }

func TestSettingsWhitelistRejectsNonEditableChanges(t *testing.T) {
	cases := []struct {
		name string
		key  string
		f    func(doc map[string]any)
	}{
		{"多角度：新增预设", "canvas_multi_angle", func(d map[string]any) {
			d["presets"] = append(presetsOf(d), map[string]any{"key": "evil", "name": "新", "rotation": 0, "tilt": 0, "zoom": "medium", "prompt": "x"})
		}},
		{"多角度：删除预设", "canvas_multi_angle", func(d map[string]any) { d["presets"] = presetsOf(d)[1:] }},
		{"多角度：改 rotation", "canvas_multi_angle", func(d map[string]any) {
			p := presetsOf(d)[0].(map[string]any)
			p["rotation"] = 123.0
			p["prompt"] = "顺带改一下提示词" // 混在合法改动里也不行
		}},
		{"多角度：只改提示词", "canvas_multi_angle", func(d map[string]any) { presetsOf(d)[0].(map[string]any)["prompt"] = "新的提示词" }},
		{"多角度：只改模板", "canvas_multi_angle", func(d map[string]any) { d["template"] = "角度：{angle}" }},
		{"打光：只改提示词", "canvas_light_control", func(d map[string]any) { presetsOf(d)[0].(map[string]any)["prompt"] = "新的提示词" }},
		{"打光：只改模板", "canvas_light_control", func(d map[string]any) { d["template"] = "打光：{light}" }},
		{"绘图预设：只改正文", "gen_image_prompt_presets", func(d map[string]any) {
			presetsOf(d)[0].(map[string]any)["value"].(map[string]any)["prompt"] = "新的提示词"
		}},
		{"多角度：改 presets[i].key", "canvas_multi_angle", func(d map[string]any) { presetsOf(d)[0].(map[string]any)["key"] = "renamed" }},
		{"打光：改 direction", "canvas_light_control", func(d map[string]any) { presetsOf(d)[0].(map[string]any)["direction"] = "上帝视角" }},
		{"打光：新增预设", "canvas_light_control", func(d map[string]any) {
			d["presets"] = append(presetsOf(d), map[string]any{"key": "evil", "name": "新", "prompt": "x"})
		}},
		{"脚本：改 styles", "canvas_script_config", func(d map[string]any) { d["styles"] = []any{map[string]any{"key": "evil", "name": "注入"}} }},
		{"后处理：加 upscaleModels", "canvas_postprocess_config", func(d map[string]any) { d["upscaleModels"] = []any{"task-doubao-video-enhance"} }},
		{"后处理：whiteModelModel 选不存在的模型", "canvas_postprocess_config", func(d map[string]any) { d["whiteModelModel"] = "x" }},
		{"后处理：whiteModelModel 选图片模型", "canvas_postprocess_config", func(d map[string]any) { d["whiteModelModel"] = "gpt-image-2" }},
		{"绘图预设：新增预设", "gen_image_prompt_presets", func(d map[string]any) {
			d["presets"] = append(presetsOf(d), map[string]any{"key": "evil", "value": map[string]any{"name": "新", "prompt": "x"}})
		}},
		{"绘图预设：删除预设", "gen_image_prompt_presets", func(d map[string]any) { d["presets"] = presetsOf(d)[1:] }},
		{"绘图预设：改 value.name", "gen_image_prompt_presets", func(d map[string]any) {
			presetsOf(d)[0].(map[string]any)["value"].(map[string]any)["name"] = "改名"
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newWhitelistEnv(t)
			before := e.raw(c.key)
			code, body := e.do("PUT", "/configs/"+c.key, e.mutate(c.key, c.f))
			if code != 400 {
				t.Fatalf("应 400，got %d %s", code, body)
			}
			if after := e.raw(c.key); after != before {
				t.Fatalf("库里的值被改了：\nbefore=%s\nafter=%s", before, after)
			}
			if e.modified(c.key) {
				t.Fatal("不应留下改动记录")
			}
		})
	}
}

func TestSettingsWhitelistAcceptsEditableChanges(t *testing.T) {
	e := newWhitelistEnv(t)
	// 多角度：只开放适用模型（提示词、模板是内置资产，不可改）。
	models := []any{"gemini-3-pro-image-preview", "gpt-image-2"}
	body := e.mutate("canvas_multi_angle", func(d map[string]any) { d["models"] = models })
	if code, out := e.do("PUT", "/configs/canvas_multi_angle", body); code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	var doc map[string]any
	json.Unmarshal([]byte(e.raw("canvas_multi_angle")), &doc)
	if got, _ := json.Marshal(doc["models"]); string(got) != `["gemini-3-pro-image-preview","gpt-image-2"]` {
		t.Fatalf("合法改动没有写入：%s", e.raw("canvas_multi_angle"))
	}
	if !e.modified("canvas_multi_angle") {
		t.Fatal("应记为已修改")
	}
	// 后处理：转白模可换成另一个视频模型，也可清空（= 未配置）。
	for _, w := range []string{"task-doubao-video-enhance", ""} {
		body := e.mutate("canvas_postprocess_config", func(d map[string]any) { d["whiteModelModel"] = w })
		if code, out := e.do("PUT", "/configs/canvas_postprocess_config", body); code != 200 {
			t.Fatalf("whiteModelModel=%q：%d %s", w, code, out)
		}
		json.Unmarshal([]byte(e.raw("canvas_postprocess_config")), &doc)
		if doc["whiteModelModel"] != w {
			t.Fatalf("whiteModelModel 没有写入 %q：%s", w, e.raw("canvas_postprocess_config"))
		}
	}
	// 原样提交（没有任何变化）→ 200，不产生新记录。
	before := e.raw("canvas_script_config")
	if code, out := e.do("PUT", "/configs/canvas_script_config", e.mutate("canvas_script_config", func(map[string]any) {})); code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	if e.raw("canvas_script_config") != before || e.modified("canvas_script_config") {
		t.Fatal("原样提交不应改库")
	}
}

func TestSettingsWhitelistGenImagePresetsRequireModels(t *testing.T) {
	e := newWhitelistEnv(t)
	before := e.raw("gen_image_prompt_presets")
	code, body := e.do("PUT", "/configs/gen_image_prompt_presets", e.mutate("gen_image_prompt_presets", func(d map[string]any) { d["models"] = []any{} }))
	if code != 400 || !strings.Contains(body, `"field":"models"`) {
		t.Fatalf("空 models 应 400：%d %s", code, body)
	}
	if e.raw("gen_image_prompt_presets") != before {
		t.Fatal("不应写库")
	}
}

func TestSettingsAgentModelRejectsExtraFields(t *testing.T) {
	e := newWhitelistEnv(t)
	code, body := e.do("PUT", "/agent-models/1", `{"modelName":"canvas-agent-gpt-5.5","max_tokens":99}`)
	if code != 400 {
		t.Fatalf("多余字段应 400：%d %s", code, body)
	}
}

// 模型补丁只认白名单字段，请求模板等一律 400（handler 严格解码），库不变。
func TestSettingsModelPatchRejectsTemplateFields(t *testing.T) {
	e := newWhitelistEnv(t)
	code, body := e.do("GET", "/models", "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var list struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal([]byte(body), &list)
	if len(list.Data) == 0 {
		t.Fatal("没有模型")
	}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(map[string]any{"name": "x", "requestTemplate": map[string]any{"evil": true}})
	if code, out := e.do("PATCH", "/models/"+jsonInt(list.Data[0].ID), buf.String()); code != 400 {
		t.Fatalf("应 400：%d %s", code, out)
	}
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }
