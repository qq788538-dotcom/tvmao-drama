package agent

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// buildUserMessage 构造当前这一轮的 user 消息。如果有任何 mention 带 URL，就
// 构造一条带 image part 的多模态消息，让 LLM 直接看到被引用的 canvas 内容。
// 否则就是纯文本。
//
// 注意：eino 的 schema.Message.MultiContent / ChatMessagePart / ChatMessageImageURL
// 已被标记为 Deprecated，官方推荐改用更新的 UserInputMultiContent +
// MessageInputPart API，但 MultiContent 仍是我们当下用的 model adapter 所支持的
// 标准路径，目前仍可正常工作。
func buildUserMessage(text string, mentions []Mention) *schema.Message {
	var imageURLs []string
	var refLines []string
	for _, m := range mentions {
		// 大文本附件（file=上传的 .txt/.md，text=画布文字节点）不内联正文也
		// 不挂媒体 part：转成引用行，planner 需要内容时自己调 read_attachment
		// 分段读。这类 mention 可以没有 URL，所以要在 URL 检查之前分流。
		if m.Kind == "file" || m.Kind == "text" {
			name := m.Name
			if name == "" {
				name = m.ArtifactID
			}
			refLines = append(refLines, attachmentRefLine(m.Kind, name, m.ArtifactID, m.CharCount))
			continue
		}
		if m.URL == "" {
			continue
		}
		switch m.Kind {
		case "video", "audio":
			// 视频/音频不能挂成 image part：多模态上游只认图片，挂上去要么 400
			// 要么被误读成损坏图（曾经一律当图片挂，@ 视频的直连通道是断的）。
			// 转成文本引用：planner 需要看内容时自己调 analyze_video 这类工具。
			name := m.Name
			if name == "" {
				name = m.ArtifactID
			}
			refLines = append(refLines, mediaRefLine(m.Kind, name, m.URL))
		default:
			// image 与空 Kind（旧客户端没这个字段）保持原行为：挂 image part。
			imageURLs = append(imageURLs, m.URL)
		}
	}
	if len(refLines) > 0 {
		text = text + "\n\n" + strings.Join(refLines, "\n")
	}
	if len(imageURLs) == 0 {
		return &schema.Message{Role: schema.User, Content: text}
	}
	parts := []schema.ChatMessagePart{
		{Type: schema.ChatMessagePartTypeText, Text: text},
	}
	for _, u := range imageURLs {
		parts = append(parts, schema.ChatMessagePart{
			Type:     schema.ChatMessagePartTypeImageURL,
			ImageURL: &schema.ChatMessageImageURL{URL: u},
		})
	}
	return &schema.Message{Role: schema.User, MultiContent: parts}
}

// mediaRefLine 渲染「用户引用了视频/音频」的文本引用行。发送轮（buildUserMessage）
// 与历史回放轮（blocksToSchemaMessage）共用同一份措辞——回放轮若自己拼一套文案，
// planner 会在同一会话里看到两种说法，且改措辞时极易只改一处。
func mediaRefLine(kind, name, url string) string {
	kindLabel := "视频"
	toolHint := "analyze_video"
	if kind == "audio" {
		kindLabel = "音频"
		toolHint = "analyze_audio"
	}
	return fmt.Sprintf("[用户引用了%s《%s》：%s ——需要了解其内容时请调用 %s]", kindLabel, name, url, toolHint)
}

// attachmentRefLine 渲染「大文本附件」的引用行。与 mediaRefLine 同一纪律：
// 发送轮（buildUserMessage）与历史回放轮（blocksToSchemaMessage）共用同一份
// 措辞，改文案只改这一处。
func attachmentRefLine(kind, name, artifactID string, charCount int64) string {
	kindLabel := "文本文件"
	if kind == "text" {
		kindLabel = "画布文本节点"
	}
	return fmt.Sprintf("[用户引用了%s《%s》（artifact_id=%s，约 %d 字）——正文未内联，需要内容时调用 %s 分段读取]", kindLabel, name, artifactID, charCount, ReadAttachmentToolName)
}
