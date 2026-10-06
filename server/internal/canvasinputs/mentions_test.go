package canvasinputs

import "testing"

// 用例与前端 PromptMentionEditor.test.tsx 的 serializeMentionPrompt 组逐字对齐
// （golden 同构测试）：同一 token 集合、同一连线顺序,两端必须产出同一段纯文本。
// 前端 refs 是 [音频 n-aud, 图片 n-img1, 图片 n-img2, 视频 n-vid],这里按
// Connected 的形状拆成三个同序 ID 列表。
func TestSerializeMentionPromptGolden(t *testing.T) {
	imgs := []string{"n-img1", "n-img2"}
	vids := []string{"n-vid"}
	auds := []string{"n-aud"}

	t.Run("token 按当前连线顺序序列化为「图片N」纯文本", func(t *testing.T) {
		got := SerializeMentionPrompt("看 @[图片:n-img2] 配 @[音频:n-aud] 和 @[视频:n-vid]", imgs, vids, auds)
		if want := "看 图片2 配 音频1 和 视频1"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("引用已断线的 token 直接剔除", func(t *testing.T) {
		if got := SerializeMentionPrompt("前@[图片:n-gone]后", imgs, vids, auds); got != "前后" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("删除前面的连线后序号跟随左移", func(t *testing.T) {
		// n-img1 断线：n-img2 从「图片2」变成「图片1」。
		if got := SerializeMentionPrompt("@[图片:n-img2]", []string{"n-img2"}, vids, auds); got != "图片1" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("kind 不匹配的同名 id 不能顶替", func(t *testing.T) {
		// n-img1 是图片,写成 @[视频:...] 视为死引用剔除（对齐前端 stripDeadMentions 的语义）。
		if got := SerializeMentionPrompt("@[视频:n-img1]", imgs, vids, auds); got != "" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("gen-image 语义:只认图片,视频/音频 token 剔除", func(t *testing.T) {
		// 对齐 GenImageNode 的 refs.filter(kind==='image')——nil 列表即「该类型无引用」。
		got := SerializeMentionPrompt("@[图片:n-img1]和@[视频:n-vid]", imgs, nil, nil)
		if want := "图片1和"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("无 token 时原样返回", func(t *testing.T) {
		if got := SerializeMentionPrompt("平平无奇的提示词", imgs, vids, auds); got != "平平无奇的提示词" {
			t.Fatalf("got %q", got)
		}
	})
}

// Connected 必须同时给出媒体 URL 与来源节点 ID,两组同序——序号翻译靠 ID 定位,
// 上游提交靠 URL,错位一个都不行。
func TestConnectedCollectsSourceNodeIDs(t *testing.T) {
	snap := Snapshot{
		Nodes: []map[string]any{
			node("i1", "input-image", "https://x/1.png", nil),
			node("a1", "input-audio", "https://x/a.mp3", nil),
			node("i2", "gen-image", "https://x/2.png", nil),
			node("v1", "video-input", "https://x/v.mp4", nil),
			node("g", "gen-video", "", nil),
		},
		Connections: []map[string]any{
			conn("c1", "i1", "g"), conn("c2", "a1", "g"), conn("c3", "i2", "g"), conn("c4", "v1", "g"),
		},
	}
	got := Connected(snap, "g", nil)
	assertStrs(t, "ImageIDs", got.ImageIDs, []string{"i1", "i2"})
	assertStrs(t, "AudioIDs", got.AudioIDs, []string{"a1"})
	assertStrs(t, "VideoIDs", got.VideoIDs, []string{"v1"})
}

func assertStrs(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
}
