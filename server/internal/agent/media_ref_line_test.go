package agent

import (
	"strings"
	"testing"
)

// 本机视频/音频的引用行会原文发给上游：回环地址上的媒体 token 不能跟着出去。
// 去掉查询串后，planner 把地址交给 analyze_video 时 ExtractKey 仍按路径认得回 key。
func TestMediaRefLineStripsLocalMediaToken(t *testing.T) {
	m := buildUserMessage("看看", []Mention{
		{ArtifactID: "v1", Kind: "video", Name: "片段", URL: "http://127.0.0.1:51234/media/u1/2026/10/a.mp4?token=SECRET#t=1"},
		{ArtifactID: "v2", Kind: "audio", Name: "配乐", URL: "https://cdn.example.com/b.mp3?sig=keep"},
	})
	if strings.Contains(m.Content, "SECRET") || strings.Contains(m.Content, "token=") {
		t.Fatalf("本机媒体 token 泄露到上游文本: %q", m.Content)
	}
	if !strings.Contains(m.Content, "http://127.0.0.1:51234/media/u1/2026/10/a.mp4 ") {
		t.Fatalf("本机地址的路径应保留: %q", m.Content)
	}
	if !strings.Contains(m.Content, "https://cdn.example.com/b.mp3?sig=keep") {
		t.Fatalf("外链的查询串（签名）必须原样保留: %q", m.Content)
	}
	// 历史回放轮共用 mediaRefLine，同样不能带 token。
	if line := mediaRefLine("video", "x", "http://localhost:9/media/k.mp4?token=S"); strings.Contains(line, "token") {
		t.Fatalf("回放引用行泄露 token: %q", line)
	}
}
