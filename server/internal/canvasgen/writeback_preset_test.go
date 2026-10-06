package canvasgen

import (
	"testing"
)

// sink 回写 history 时,pendingGen 里的预设快照(presetKey/presetName)必须随
// prompt 一起进条目——与前端 appendHistoryItem 同构。漏掉的话「提交完就关页面」
// 的路径下历史条目永远缺预设信息(正常路径就是 sink 写的)。
func TestAppendHistoryCarriesPresetSnapshot(t *testing.T) {
	node := map[string]any{
		"id": "n1",
		"settings": map[string]any{
			"pendingGen": map[string]any{
				"prompt":     "画只猫",
				"presetKey":  "three_view",
				"presetName": "角色三视图",
			},
		},
	}
	appendHistory(node, "https://cdn/x.png", "2026-08-14T00:00:00Z")
	settings := node["settings"].(map[string]any)
	items := settings["history"].([]any)
	item := items[len(items)-1].(map[string]any)
	if item["presetKey"] != "three_view" || item["presetName"] != "角色三视图" {
		t.Fatalf("预设快照没进历史: %v", item)
	}
	if _, ok := settings["pendingGen"]; ok {
		t.Fatalf("pendingGen 应用完即删")
	}
}
