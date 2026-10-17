package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// 这组测试钉住「附件跨轮存活」：user 消息落库时带 image/video/audio block，
// 历史回放（blocksToSchemaMessage）必须把它们还原给 planner——video/audio 还原成
// 与发送轮 buildUserMessage 同款的文本引用行，image 还原成 image part。曾经回放
// 只认 skill/text，附件在携带它的那一轮之后就"失忆"：用户发视频 → agent 反问
// 确认 → 用户说"是的" → planner 手里只剩文件名。

func TestBlocksToSchemaMessage_UserVideoBlockSurvivesReplay(t *testing.T) {
	msgs := blocksToSchemaMessage("user", []ContentBlock{
		{Type: "text", Text: "@demo.mp4 复刻这个视频"},
		{Type: "video", URL: "https://x/v.mp4", Name: "demo.mp4"},
	})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.Role != schema.User {
		t.Fatalf("role: %v", m.Role)
	}
	if !strings.Contains(m.Content, "https://x/v.mp4") {
		t.Fatalf("video URL lost in replay, content=%q", m.Content)
	}
	if !strings.Contains(m.Content, "analyze_video") {
		t.Fatalf("expected analyze_video hint, content=%q", m.Content)
	}
	if !strings.Contains(m.Content, "demo.mp4") {
		t.Fatalf("expected video name, content=%q", m.Content)
	}
	// 与发送轮措辞一致：同一 kind/name/url 应产出同一行。
	want := mediaRefLine("video", "demo.mp4", "https://x/v.mp4")
	if !strings.Contains(m.Content, want) {
		t.Fatalf("replay wording diverged from send-turn wording:\nwant substring %q\ngot %q", want, m.Content)
	}
}

func TestBlocksToSchemaMessage_UserAudioBlockSurvivesReplay(t *testing.T) {
	msgs := blocksToSchemaMessage("user", []ContentBlock{
		{Type: "text", Text: "听下这个"},
		{Type: "audio", URL: "https://x/a.mp3", Name: "a.mp3"},
	})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "https://x/a.mp3") {
		t.Fatalf("audio URL lost in replay, content=%q", msgs[0].Content)
	}
}

func TestBlocksToSchemaMessage_UserImageBlockSurvivesReplay(t *testing.T) {
	msgs := blocksToSchemaMessage("user", []ContentBlock{
		{Type: "text", Text: "看这张图"},
		{Type: "image", URL: "https://x/p.png", Name: "p.png"},
	})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if len(m.MultiContent) != 2 {
		t.Fatalf("expected 2 parts (text + image), got %d: %+v", len(m.MultiContent), m.MultiContent)
	}
	if m.MultiContent[0].Type != schema.ChatMessagePartTypeText || m.MultiContent[0].Text != "看这张图" {
		t.Fatalf("text part mismatch: %+v", m.MultiContent[0])
	}
	if m.MultiContent[1].Type != schema.ChatMessagePartTypeImageURL || m.MultiContent[1].ImageURL == nil || m.MultiContent[1].ImageURL.URL != "https://x/p.png" {
		t.Fatalf("image part mismatch: %+v", m.MultiContent[1])
	}
}

// URL 为空的媒体 block（理论上不该落库，防御性）不产出引用行，也不挂空 part。
func TestBlocksToSchemaMessage_UserMediaBlockWithoutURLIgnored(t *testing.T) {
	msgs := blocksToSchemaMessage("user", []ContentBlock{
		{Type: "text", Text: "plain"},
		{Type: "video", URL: "", Name: "ghost.mp4"},
		{Type: "image", URL: "", Name: "ghost.png"},
	})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "plain" || msgs[0].MultiContent != nil {
		t.Fatalf("expected untouched plain message, got content=%q multi=%+v", msgs[0].Content, msgs[0].MultiContent)
	}
}
