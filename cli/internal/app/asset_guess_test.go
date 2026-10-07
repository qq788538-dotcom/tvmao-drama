package app

import "testing"

// 上传时的输入节点类型推断。视频曾经不在推断表里——upload 明明能把 mp4 传上去，
// --create-node 却报「无法判断素材类型」，用户以为视频不支持上传。
func TestGuessInputNodeType(t *testing.T) {
	cases := []struct {
		path, mime, want string
	}{
		{"a.png", "image/png", "image-input"},
		{"a.mp3", "audio/mpeg", "audio-input"},
		{"a.mp4", "video/mp4", "video-input"},
		// MIME 缺失时按扩展名兜底（清单与 canvasinputs 的 videoExtRe 同集合）。
		{"b.mp4", "", "video-input"},
		{"b.webm", "", "video-input"},
		{"b.mov", "", "video-input"},
		{"b.wav", "", "audio-input"},
		{"b.jpg", "", "image-input"},
		// 判断不了返回空串，让调用方要求显式 --node-type。
		{"b.bin", "application/octet-stream", ""},
	}
	for _, c := range cases {
		if got := guessInputNodeType(c.path, c.mime); got != c.want {
			t.Errorf("guessInputNodeType(%q, %q) = %q, want %q", c.path, c.mime, got, c.want)
		}
	}
}
