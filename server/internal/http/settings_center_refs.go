package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// settingsConfigKeys 是五项内置画布配置（settings 表的 key）。写死为内置值、设置页不展示，
// 这里只用于模型中心标注模型被哪些配置引用（modelReferences）。
var settingsConfigKeys = []string{
	"canvas_postprocess_config",
	"gen_image_prompt_presets",
	"canvas_multi_angle",
	"canvas_light_control",
	"canvas_script_config",
}

var settingsConfigLabels = map[string]string{
	"canvas_postprocess_config": "视频后处理",
	"gen_image_prompt_presets":  "绘图预设",
	"canvas_multi_angle":        "多角度",
	"canvas_light_control":      "打光",
	"canvas_script_config":      "剧本生成分镜",
}

// modelRef 是配置里对模型中心某个 modelId 的一处引用。
type modelRef struct {
	Path     string // 例如 models[0]、upscaleModels[1].modelId
	ModelID  string
	WantType string // image / video / text
}

// extractModelRefs 找出一项配置里引用的全部模型（空字符串跳过）。raw 为空返回 nil。
// 字段位置与 canvasgen / canvasscript 的配置结构一致。
func extractModelRefs(key string, raw []byte) ([]modelRef, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	var out []modelRef
	add := func(path string, x any, want string) {
		if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, modelRef{Path: path, ModelID: s, WantType: want})
		}
	}
	list := func(field string) []any {
		a, _ := v[field].([]any)
		return a
	}
	switch key {
	case "gen_image_prompt_presets", "canvas_multi_angle", "canvas_light_control":
		for i, m := range list("models") {
			add(fmt.Sprintf("models[%d]", i), m, "image")
		}
	case "canvas_postprocess_config":
		for _, f := range []string{"subtitleRemovalModels", "upscaleModels"} {
			for i, b := range list(f) {
				if o, ok := b.(map[string]any); ok {
					add(fmt.Sprintf("%s[%d].modelId", f, i), o["modelId"], "video")
				}
			}
		}
		add("upscaleModel", v["upscaleModel"], "video")
		add("whiteModelModel", v["whiteModelModel"], "video")
	case "canvas_script_config":
		for i, c := range list("modelCandidates") {
			if o, ok := c.(map[string]any); ok {
				add(fmt.Sprintf("modelCandidates[%d].modelId", i), o["modelId"], "text")
			}
		}
	}
	return out, nil
}
