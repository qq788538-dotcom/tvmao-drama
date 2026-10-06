package generation

import "testing"

// bug: 隐藏模型(如 sd2.0 去字幕)不进 /models 列表 → 前端历史面板拿不到
// model.type，回退看 URL 扩展名；而产物 key 是 gen/task-N-0（无 .mp4 后缀）→
// 误判成图片。修法：finalize 把主产物 mime 的大类写进 output.outputKind，
// 前端优先读它，不必再靠 model 目录或 URL 后缀猜。
func TestOutputKindFromMime(t *testing.T) {
	cases := []struct {
		mime string
		want string
	}{
		{"video/mp4", "video"},
		{"video/quicktime", "video"},
		{"image/png", "image"},
		{"image/jpeg", "image"},
		{"audio/mpeg", "audio"},
		{"", ""},
		{"application/octet-stream", ""},
	}
	for _, c := range cases {
		if got := outputKindFromMime(c.mime); got != c.want {
			t.Errorf("outputKindFromMime(%q) = %q, want %q", c.mime, got, c.want)
		}
	}
}
