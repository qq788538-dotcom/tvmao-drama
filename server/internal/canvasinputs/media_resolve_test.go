package canvasinputs

import (
	"reflect"
	"strings"
	"testing"
)

// 生产事故回归:快照持久化存 canonical 的 media://key(躲签名 URL 24h 过期),
// 浏览器 store 在加载时已把它签成 https,所以网页派生看到的恒是 http;服务端
// Connected 却拿 canonical 直接过 http 过滤——media:// 活不到 sign 那步,已生成
// gen-image 节点被整个丢弃,CLI 提交生视频 images 为空(test 环境任务 795 实锤,
// 同节点网页任务 796 带 2 图)。同构的前提是先把表示对齐:先 sign 再过滤/分类。
func TestConnectedResolvesCanonicalMediaContent(t *testing.T) {
	// 模拟 MediaSigner.Sign:认识 media:// 就换成签名 https,其它原样返回。
	sign := func(s string) string {
		if strings.HasPrefix(s, "media://") {
			return "https://signed.example.com/" + strings.TrimPrefix(s, "media://") + "?sig=abc"
		}
		return s
	}
	snap := Snapshot{
		Nodes: []map[string]any{
			node("g1", "gen-image", "media://tapnow_test/u5/gen_task-793-0", map[string]any{"kind": "image"}),
			node("g2", "gen-image", "media://tapnow_test/u5/gen_task-794-0", map[string]any{"kind": "image"}),
			node("empty", "gen-image", "", nil), // 还没生成出内容,两端都跳过
			node("v", "gen-video", "", nil),
		},
		Connections: []map[string]any{
			conn("c1", "g1", "v"), conn("c2", "g2", "v"), conn("c3", "empty", "v"),
		},
	}
	got := Connected(snap, "v", sign)
	wantImgs := []string{
		"https://signed.example.com/tapnow_test/u5/gen_task-793-0?sig=abc",
		"https://signed.example.com/tapnow_test/u5/gen_task-794-0?sig=abc",
	}
	if !reflect.DeepEqual(got.Images, wantImgs) {
		t.Fatalf("Images = %v, want %v", got.Images, wantImgs)
	}
	if !reflect.DeepEqual(got.ImageIDs, []string{"g1", "g2"}) {
		t.Fatalf("ImageIDs = %v", got.ImageIDs)
	}
}

// media:// 的视频/音频产物同样要先解析再分类——分类正则看的是签名后带扩展名的
// URL,与浏览器(store 里就是签名 URL)一致。
func TestConnectedResolvedMediaClassification(t *testing.T) {
	sign := func(s string) string {
		if strings.HasPrefix(s, "media://") {
			return "https://signed.example.com/" + strings.TrimPrefix(s, "media://") + "?sig=abc"
		}
		return s
	}
	snap := Snapshot{
		Nodes: []map[string]any{
			node("a", "input-audio", "media://u5/voice.mp3", nil),
			node("p", "preview", "media://u5/clip.mp4", map[string]any{"kind": "video"}),
			node("v", "gen-video", "", nil),
		},
		Connections: []map[string]any{conn("c1", "a", "v"), conn("c2", "p", "v")},
	}
	got := Connected(snap, "v", sign)
	if len(got.Audios) != 1 || !strings.Contains(got.Audios[0], "voice.mp3") {
		t.Fatalf("Audios = %v", got.Audios)
	}
	if len(got.Videos) != 1 || !strings.Contains(got.Videos[0], "clip.mp4") {
		t.Fatalf("Videos = %v", got.Videos)
	}
}
