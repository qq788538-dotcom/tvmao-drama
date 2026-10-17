package cliapi

import "testing"

// gen-image 的绘图提示词预设在画布里存 settings.promptPreset（网页 GenImageNode 写入的
// key）。CLI 不把它当一等参数——但它必须作为未知字段在 params↔settings 往返里原样
// 存活：RunNode 的写回走 mergeSettingsPatch(settingsFromParams(effective))，若字段在
// 往返中被吃掉/改名，CLI 每 run 一次就会抹掉网页选好的预设，且没人报错。
// 同 video_prompt_test.go 的道理：canvasinputs 同构测试够不到存储字段名这一层。
func TestPromptPresetSettingSurvivesCLIRoundTrip(t *testing.T) {
	settings := map[string]any{"prompt": "画只猫", "promptPreset": "three_view"}
	params := paramsFromSettings("gen-image", settings)
	if params["promptPreset"] != "three_view" {
		t.Fatalf("promptPreset 没透传进 params: %v", params)
	}
	back := mergeSettingsPatch("gen-image", params, map[string]any{"pendingGen": map[string]any{}})
	if back["promptPreset"] != "three_view" {
		t.Fatalf("RunNode 写回会抹掉 promptPreset: %v", back)
	}
	if back["prompt"] != "画只猫" {
		t.Fatalf("prompt 变形: %v", back)
	}
}

// presetParams 同 promptPreset：必须在 CLI 的 params↔settings 往返里原样存活，
// 否则 CLI 每 run 一次就抹掉网页选好的参数档位。
func TestPresetParamsSettingSurvivesCLIRoundTrip(t *testing.T) {
	settings := map[string]any{"promptPreset": "portrait", "presetParams": map[string]any{"skin": "real"}}
	params := paramsFromSettings("gen-image", settings)
	back := mergeSettingsPatch("gen-image", params, nil)
	pp, _ := back["presetParams"].(map[string]any)
	if pp == nil || pp["skin"] != "real" {
		t.Fatalf("presetParams 往返丢失: %v", back)
	}
}
