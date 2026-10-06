package http

import (
	"encoding/json"
	"strings"
	"testing"
)

type configResp struct {
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
	Modified bool            `json:"modified"`
}

func TestConfigGet(t *testing.T) {
	e := newSCEnv(t)
	code, body := e.do("GET", "/configs/canvas_multi_angle", nil)
	c := decodeTo[configResp](t, body)
	if code != 200 || c.Key != "canvas_multi_angle" || c.Modified || !strings.Contains(string(c.Value), "鱼眼镜头") {
		t.Fatalf("%d %s", code, body)
	}
	code, body = e.do("GET", "/configs/canvas_light_control", nil)
	if c := decodeTo[configResp](t, body); code != 200 || string(c.Value) != "null" {
		t.Fatalf("未内置的配置应返回 null: %d %s", code, body)
	}
	for _, k := range []string{"upstream_api_key", "base_domain", "drama_agent_config"} {
		if code, _ := e.do("GET", "/configs/"+k, nil); code != 404 {
			t.Errorf("%s 应 404，got %d", k, code)
		}
	}
}

func TestConfigPutSavesThroughEditService(t *testing.T) {
	e := newSCEnv(t)
	v := `{"models":["gpt-image-2"],"template":"新模板 {angle}","presets":[{"key":"fisheye","name":"鱼眼","rotation":0,"tilt":0,"zoom":"medium","prompt":"新提示词"}]}`
	code, body := e.do("PUT", "/configs/canvas_multi_angle", `{"value":`+v+`}`)
	c := decodeTo[configResp](t, body)
	if code != 200 || !c.Modified || !strings.Contains(string(c.Value), "新提示词") {
		t.Fatalf("%d %s", code, body)
	}
	stored, _ := e.settings.Get("canvas_multi_angle")
	if !strings.Contains(stored, "新模板 {angle}") || strings.Contains(stored, "\n") {
		t.Fatalf("应存紧凑 JSON: %s", stored)
	}
}

func TestConfigPutRejectsBadModelRefs(t *testing.T) {
	e := newSCEnv(t)
	cases := map[string]string{
		"canvas_multi_angle":        `{"models":["no-such"],"template":"","presets":[]}`,
		"gen_image_prompt_presets":  `{"models":["canvas-gpt-5.5"],"presets":[]}`, // text 模型不能当绘图模型
		"canvas_script_config":      `{"modelCandidates":[{"modelId":"old-text","displayName":"x"}],"styles":[],"concurrency":1,"maxRetries":1}`, // 已停用
		"canvas_postprocess_config": `{"subtitleRemovalModels":[],"upscaleModel":"gpt-image-2"}`,
		"canvas_light_control":      `{"models":[""],"template":"","presets":[]}`,
	}
	for key, v := range cases {
		code, body := e.do("PUT", "/configs/"+key, `{"value":`+v+`}`)
		if code != 400 {
			t.Errorf("%s → %d %s", key, code, body)
		}
	}
	if len(e.settingEdit.modified) != 0 {
		t.Fatal("被拒的请求不应写入")
	}
}

func TestConfigPutRejectsBadPromptsAndNumbers(t *testing.T) {
	e := newSCEnv(t)
	cases := map[string]string{
		"gen_image_prompt_presets": `{"models":["gpt-image-2"],"presets":[{"key":"portrait","value":{"name":"人像","prompt":"  "}}]}`,
		"canvas_multi_angle":       `{"models":["gpt-image-2"],"template":"没有占位符","presets":[]}`,
		"canvas_light_control":     `{"models":["gpt-image-2"],"template":"没有占位符","presets":[]}`,
		"canvas_script_config":     `{"modelCandidates":[],"styles":[],"concurrency":21,"maxRetries":1}`,
	}
	for key, v := range cases {
		if code, body := e.do("PUT", "/configs/"+key, `{"value":`+v+`}`); code != 400 {
			t.Errorf("%s → %d %s", key, code, body)
		}
	}
	for _, raw := range []string{`{"value":[1,2]}`, `{"value":"x"}`, `{"value":null}`, `{"value":{},"extra":1}`, `not json`} {
		if code, _ := e.do("PUT", "/configs/canvas_multi_angle", raw); code != 400 {
			t.Errorf("%s → %d", raw, code)
		}
	}
}

func TestConfigPutPassesEditServiceRejection(t *testing.T) {
	e := newSCEnv(t)
	e.settingEdit.reject["canvas_script_config"] = &SettingsValidationError{Field: "styles", Message: "风格配置不允许修改"}
	code, body := e.do("PUT", "/configs/canvas_script_config", `{"value":`+scScriptSeed+`}`)
	if code != 400 || !strings.Contains(body, "风格配置不允许修改") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestConfigRestoreDefault(t *testing.T) {
	e := newSCEnv(t)
	e.do("PUT", "/configs/canvas_multi_angle", `{"value":{"models":[],"template":"","presets":[]}}`)
	code, body := e.do("POST", "/configs/canvas_multi_angle/restore-default", nil)
	c := decodeTo[configResp](t, body)
	if code != 200 || c.Modified || !strings.Contains(string(c.Value), "鱼眼镜头") {
		t.Fatalf("%d %s", code, body)
	}
	if code, _ := e.do("POST", "/configs/canvas_light_control/restore-default", nil); code != 404 {
		t.Fatalf("没有内置值时应 404，got %d", code)
	}
}
