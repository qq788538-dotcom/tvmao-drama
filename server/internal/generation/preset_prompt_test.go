package generation

import (
	"encoding/json"
	"testing"
)

// 拼接规则：预设正文在前 + "\n" + 用户 prompt；用户 prompt 为空只用正文（无尾随换行）。
// 必须拷贝 map——入参可能是任务快照的引用，原地改写会污染后续重试的重建。
func TestApplyPresetPrompt(t *testing.T) {
	orig := map[string]any{"prompt": "画只猫", "size": "2K"}
	got := applyPresetPrompt(orig, "白底三视图")
	if got["prompt"] != "白底三视图\n画只猫" {
		t.Fatalf("prompt = %q", got["prompt"])
	}
	if got["size"] != "2K" {
		t.Fatalf("其余字段要保留: %v", got)
	}
	if orig["prompt"] != "画只猫" {
		t.Fatalf("不许改原 map: %v", orig["prompt"])
	}

	empty := applyPresetPrompt(map[string]any{}, "白底三视图")
	if empty["prompt"] != "白底三视图" {
		t.Fatalf("空用户 prompt 应只用正文: %q", empty["prompt"])
	}
}

// 快照往返：presetKey 随 input 快照持久化；未设置时不落键（存量快照形状不变）。
func TestTaskSnapshotPresetKeyRoundTrip(t *testing.T) {
	var snap taskSnapshot
	if err := json.Unmarshal([]byte(`{"modelId":"m","inputs":{"prompt":"x"},"presetKey":"three_view"}`), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.PresetKey != "three_view" {
		t.Fatalf("PresetKey = %q", snap.PresetKey)
	}
	blob, _ := json.Marshal(taskSnapshot{ModelID: "m", Inputs: map[string]any{}})
	if string(blob) != `{"modelId":"m","inputs":{},"note":""}` {
		t.Fatalf("空 presetKey 不该出现在序列化结果: %s", blob)
	}
}
