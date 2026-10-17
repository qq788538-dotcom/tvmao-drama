package cliapi

import (
	"encoding/json"
	"testing"
)

// gen-video 的提示词在画布里存 settings.videoPrompt，不是 settings.prompt。
//
// 这不是可选风格，是**浏览器定的既有格式**：
//
//	apps/web/src/modules/canvas/editor/nodes/GenVideoNode.tsx:100
//	  const prompt = (node.settings?.videoPrompt as string | undefined) ?? '';
//
// 它读写的都是 videoPrompt，生产里所有网页/skill 建的视频节点也都是它。
// CLI 曾经直写 prompt，于是同一个节点两边各读各的字段：CLI 建的节点在网页上
// 提示词一片空白、点生成拿到空 prompt 悄悄回退到上游文本；反过来网页建的节点
// CLI 也读不到，node run 同样跑空提示词。gen-image 两边都是 prompt，
// 所以当时只有视频中招、且一直没人发现。
//
// 下面这组测试盯的是**节点存储字段名**——canvasinputs 那套同构测试只覆盖了
// 生成入参的三步派生，够不到这一层，这正是当初漏掉的缝。
const webVideoPromptSettingKey = "videoPrompt"

func settingsOf(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var node map[string]any
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatal(err)
	}
	s, _ := node["settings"].(map[string]any)
	return s
}

func TestBuildNodeJSONStoresVideoPromptUnderWebKey(t *testing.T) {
	raw, err := BuildNodeJSON(CreateNodeRequest{
		Type: "video-generator",
		Params: map[string]any{
			"modelId": "doubao-seedance-2-0-260128",
			"prompt":  "镜头缓慢推进，逆光",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := settingsOf(t, raw)
	if got := s[webVideoPromptSettingKey]; got != "镜头缓慢推进，逆光" {
		t.Fatalf("settings.%s = %v，网页就是从这个键读提示词的", webVideoPromptSettingKey, got)
	}
	// 不能两个键都写：网页只看 videoPrompt，多出来的 prompt 是没人读的第二真源，
	// 迟早跟它不一致。
	if _, ok := s["prompt"]; ok {
		t.Fatalf("gen-video 不该再写 settings.prompt：%v", s)
	}
}

// gen-image 必须保持 prompt 原样——别把翻译误伤到图片节点上。
func TestBuildNodeJSONKeepsImagePromptKey(t *testing.T) {
	raw, err := BuildNodeJSON(CreateNodeRequest{
		Type:   "image-generator",
		Params: map[string]any{"modelId": "doubao-seedream", "prompt": "雨后城市"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := settingsOf(t, raw)
	if s["prompt"] != "雨后城市" {
		t.Fatalf("gen-image 的提示词必须存 settings.prompt：%v", s)
	}
	if _, ok := s[webVideoPromptSettingKey]; ok {
		t.Fatalf("gen-image 不该出现 videoPrompt：%v", s)
	}
}

func TestBuildNodePatchTranslatesVideoPrompt(t *testing.T) {
	raw, err := BuildNodePatch("gen-video", PatchNodeRequest{
		Params: map[string]any{"prompt": "改过的词"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	s, _ := p["settings"].(map[string]any)
	if s[webVideoPromptSettingKey] != "改过的词" {
		t.Fatalf("patch 没翻成 videoPrompt：%v", s)
	}
	if _, ok := s["prompt"]; ok {
		t.Fatalf("patch 不该留下 settings.prompt：%v", s)
	}
}

// 读出方向：网页建的视频节点（settings.videoPrompt）经 CLI 出去必须是 params.prompt。
// 对 CLI 用户暴露的始终是 prompt——那是模型 inputSchema 里的入参名。
func TestToNodeExposesVideoPromptAsPrompt(t *testing.T) {
	n := ToNode(map[string]any{
		"id":   "n-1",
		"type": "gen-video",
		"settings": map[string]any{
			"modelId":                "doubao-seedance-2-0-260128",
			webVideoPromptSettingKey: "网页写的词",
		},
	}, nil)
	if n.Params["prompt"] != "网页写的词" {
		t.Fatalf("params.prompt = %v，CLI 对外只认 prompt", n.Params["prompt"])
	}
	if _, ok := n.Params[webVideoPromptSettingKey]; ok {
		t.Fatalf("画布内部键名不该漏给调用方：%v", n.Params)
	}
}

// 两个键并存时结果必须确定。存量里有一批 CLI 早期写的裸 prompt，网页又可能
// 在同一节点上写过 videoPrompt；不定死优先级的话，同一个节点每次读出来都可能
// 不一样（map 遍历顺序）。videoPrompt 是网页认的那个，它说了算。
func TestParamsFromSettingsPrefersVideoPromptOverLegacyPrompt(t *testing.T) {
	settings := map[string]any{
		"prompt":                 "早期 CLI 写的",
		webVideoPromptSettingKey: "网页写的",
	}
	// map 遍历顺序随机，跑多次才能暴露不确定性。
	for i := 0; i < 50; i++ {
		got := paramsFromSettings("gen-video", settings)["prompt"]
		if got != "网页写的" {
			t.Fatalf("第 %d 次读出 %v，videoPrompt 必须恒胜", i, got)
		}
	}
}

// 只有裸 prompt 的存量节点（数据还没迁移）仍要读得出来，不能因为改了映射就变空。
func TestParamsFromSettingsFallsBackToLegacyPrompt(t *testing.T) {
	got := paramsFromSettings("gen-video", map[string]any{"prompt": "早期 CLI 写的"})
	if got["prompt"] != "早期 CLI 写的" {
		t.Fatalf("存量裸 prompt 读不出来了：%v", got)
	}
}

// 往返恒等：CLI 参数 → 画布 settings → CLI 参数，值和键都不能变形。
// 这条比单向断言更能守住——将来谁加了新的字段翻译，只要不对称就会红。
func TestVideoPromptRoundTripIsIdentity(t *testing.T) {
	for _, internal := range []string{"gen-video", "gen-image", "input-image"} {
		in := map[string]any{"prompt": "一段词", "modelId": "m-1", "ratio": "9:16"}
		back := paramsFromSettings(internal, settingsFromParams(internal, in))
		if len(back) != len(in) {
			t.Fatalf("%s 往返后键数变了：%v → %v", internal, in, back)
		}
		for k, v := range in {
			if back[k] != v {
				t.Fatalf("%s 往返后 %s = %v，want %v", internal, k, back[k], v)
			}
		}
	}
}
