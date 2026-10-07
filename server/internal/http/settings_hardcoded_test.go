package http

// 2026-10-07 用户要求：视频后处理、绘图预设、多角度、打光、剧本生成分镜五项配置与 Agent 模型写死为内置值。
// /api/v1/settings 下这些页面的读写接口全部注销（只有设置页在用）；画布运行时的读取接口另见 TestRuntimeConfigRoutesStillServed。

import (
	"encoding/json"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

var hardcodedConfigKeys = []string{
	"canvas_postprocess_config", "gen_image_prompt_presets", "canvas_multi_angle", "canvas_light_control", "canvas_script_config",
}

func TestHardcodedSettingsRoutesAreGone(t *testing.T) {
	e := newWhitelistEnv(t)
	before := map[string]string{}
	for _, k := range hardcodedConfigKeys {
		before[k] = e.raw(k)
	}
	type call struct{ method, path, body string }
	var calls []call
	for _, k := range hardcodedConfigKeys {
		calls = append(calls,
			call{"GET", "/configs/" + k, ""},
			call{"PUT", "/configs/" + k, `{"value":{"models":[]}}`},
			call{"PATCH", "/configs/" + k, `{"value":{"models":[]}}`},
			call{"POST", "/configs/" + k + "/restore-default", ""},
		)
	}
	calls = append(calls,
		call{"GET", "/agent-models", ""},
		call{"PUT", "/agent-models/1", `{"modelName":"gpt-5.5"}`},
		call{"PATCH", "/agent-models/1", `{"modelName":"gpt-5.5"}`},
		call{"POST", "/agent-models/1/restore-default", ""},
	)
	for _, c := range calls {
		if code, body := e.do(c.method, c.path, c.body); code != nethttp.StatusNotFound && code != nethttp.StatusMethodNotAllowed {
			t.Errorf("%s %s 应 404/405，got %d %s", c.method, c.path, code, body)
		}
	}
	for _, k := range hardcodedConfigKeys {
		if e.raw(k) != before[k] || e.modified(k) {
			t.Errorf("%s 被改动", k)
		}
	}
}

// 设置页剩下的接口照常：通用、模型中心（字段编辑 + 恢复默认）。
func TestRemainingSettingsRoutesStillWork(t *testing.T) {
	e := newWhitelistEnv(t)
	if code, body := e.do("GET", "/general", ""); code != 200 {
		t.Fatalf("GET /general：%d %s", code, body)
	}
	code, body := e.do("GET", "/models", "")
	if code != 200 {
		t.Fatalf("GET /models：%d %s", code, body)
	}
	var list struct {
		Data []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil || len(list.Data) == 0 {
		t.Fatalf("模型列表：%v %s", err, body)
	}
	id := jsonInt(list.Data[0].ID)
	if code, body := e.do("PATCH", "/models/"+id, `{"name":"我的模型"}`); code != 200 {
		t.Fatalf("PATCH 模型名：%d %s", code, body)
	}
	if code, body := e.do("POST", "/models/"+id+"/restore-default", ""); code != 200 {
		t.Fatalf("恢复默认：%d %s", code, body)
	}
}

// 画布运行时读取配置的接口不受影响（它们不在 /api/v1/settings 下，由 NewRouter 注册）。
func TestRuntimeConfigRoutesStillServed(t *testing.T) {
	h := NewRouter(Deps{CanvasScript: &CanvasScriptHandler{}})
	routes := map[string]bool{}
	if err := chi.Walk(h.(chi.Routes), func(method, route string, _ nethttp.Handler, _ ...func(nethttp.Handler) nethttp.Handler) error {
		routes[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{
		"GET /api/v1/canvas/postprocess-config",
		"GET /api/v1/canvas/gen-image-presets",
		"GET /api/v1/canvas/multi-angle",
		"GET /api/v1/canvas/light-control",
		"GET /api/v1/canvas-script/styles",
		"POST /api/v1/canvas-script-tasks",
	} {
		if !routes[r] {
			t.Errorf("%s 应仍注册", r)
		}
	}
}
