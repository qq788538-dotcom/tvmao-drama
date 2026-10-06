package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestBuildUserMessage_TextOnly(t *testing.T) {
	m := buildUserMessage("hello", nil)
	if m == nil {
		t.Fatal("nil message")
	}
	if m.Role != schema.User {
		t.Fatalf("role: %v", m.Role)
	}
	if m.Content != "hello" {
		t.Fatalf("content: %q", m.Content)
	}
	if m.MultiContent != nil {
		t.Fatalf("expected nil MultiContent, got %+v", m.MultiContent)
	}
}

func TestBuildUserMessage_WithMentions(t *testing.T) {
	mentions := []Mention{
		{ArtifactID: "a1", URL: "http://x/a.png", Name: "A"},
		{ArtifactID: "a2", URL: "http://x/b.png", Name: "B"},
	}
	m := buildUserMessage("ref these", mentions)
	if m == nil {
		t.Fatal("nil message")
	}
	if m.Role != schema.User {
		t.Fatalf("role: %v", m.Role)
	}
	if len(m.MultiContent) != 3 {
		t.Fatalf("expected 3 parts (1 text + 2 image), got %d: %+v", len(m.MultiContent), m.MultiContent)
	}
	if m.MultiContent[0].Type != schema.ChatMessagePartTypeText || m.MultiContent[0].Text != "ref these" {
		t.Fatalf("text part mismatch: %+v", m.MultiContent[0])
	}
	if m.MultiContent[1].Type != schema.ChatMessagePartTypeImageURL || m.MultiContent[1].ImageURL == nil || m.MultiContent[1].ImageURL.URL != "http://x/a.png" {
		t.Fatalf("image1 mismatch: %+v", m.MultiContent[1])
	}
	if m.MultiContent[2].Type != schema.ChatMessagePartTypeImageURL || m.MultiContent[2].ImageURL == nil || m.MultiContent[2].ImageURL.URL != "http://x/b.png" {
		t.Fatalf("image2 mismatch: %+v", m.MultiContent[2])
	}
}

func TestBuildUserMessage_MentionsWithoutURL(t *testing.T) {
	mentions := []Mention{
		{ArtifactID: "a1", URL: "", Name: "A"},
	}
	m := buildUserMessage("plain", mentions)
	if m == nil {
		t.Fatal("nil message")
	}
	if m.Role != schema.User || m.Content != "plain" {
		t.Fatalf("role/content mismatch: role=%v content=%q", m.Role, m.Content)
	}
	if m.MultiContent != nil {
		t.Fatalf("expected nil MultiContent fallback, got %+v", m.MultiContent)
	}
}


// 大文本附件（file=上传文件 / text=画布文字节点）不内联不挂媒体 part：转成
// 带 artifact_id + 字数 + 工具名的引用行，且没有 URL 也必须生效（文字节点
// mention 本来就无 URL——这条曾被「URL 为空直接 continue」挡在门外）。
func TestBuildUserMessage_LargeTextAttachmentRefLines(t *testing.T) {
	msg := buildUserMessage("帮我改写", []Mention{
		{ArtifactID: "asset_12", Name: "剧本.txt", Kind: "file", CharCount: 420000},
		{ArtifactID: "n-abc", Name: "第一章", Kind: "text", CharCount: 98000},
	})
	if msg.MultiContent != nil {
		t.Fatalf("must stay plain text, got multicontent: %+v", msg.MultiContent)
	}
	for _, want := range []string{
		"文本文件《剧本.txt》", "artifact_id=asset_12", "420000",
		"画布文本节点《第一章》", "artifact_id=n-abc", "98000",
		ReadAttachmentToolName,
	} {
		if !strings.Contains(msg.Content, want) {
			t.Fatalf("ref line missing %q in:\n%s", want, msg.Content)
		}
	}
}
