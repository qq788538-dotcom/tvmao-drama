package canvasinputs

import (
	"fmt"
	"regexp"
	"strings"
)

// 提示词 @ 提及的服务端序列化。
//
//	mentionRe               ↔ apps/web/.../editor/utils/mentions.ts           MENTION_RE
//	SerializeMentionPrompt  ↔ apps/web/.../editor/PromptMentionEditor.tsx     serializeMentionPrompt
//
// 网页把提示词以 canonical 形式落库：@[图片:<节点ID>]（另有 视频/音频），提交
// 生成的瞬间才按当前连线里同类型的位置翻译成「图片2」这种上游模型认识的纯文本。
// 这层翻译历史上只在浏览器里有——CLI 走 node run 时 token 会原样进上游 prompt，
// 模型收到一串它看不懂的内部 ID。所以服务端必须有同一份翻译，且语义逐字同构：
// 同一节点同一提示词，CLI 提交与网页提交产出一致（与本包三步派生同一条验收铁律）。
var mentionRe = regexp.MustCompile(`@\[(图片|视频|音频):([^\]]+)\]`)

// SerializeMentionPrompt 把 @[类型:节点ID] 翻译成「图片N」纯文本。
// N 是该节点在同类型连线里的 1 起位置（图片/视频/音频各自独立计数），
// 引用不在列表里的 token 直接剔除——与前端「断线 token 提交前兜底剔除」一致。
// 类型不匹配的同名 id 也算死引用（图片节点写成 @[视频:…] 不能顶替）。
//
// gen-image 只认图片引用（GenImageNode 的 refs.filter(kind==='image')），
// 调用方传 nil 的 videoIDs/audioIDs 即可让视频/音频 token 走剔除分支。
func SerializeMentionPrompt(prompt string, imageIDs, videoIDs, audioIDs []string) string {
	if !strings.Contains(prompt, "@[") {
		return prompt
	}
	idsByLabel := map[string][]string{"图片": imageIDs, "视频": videoIDs, "音频": audioIDs}
	return mentionRe.ReplaceAllStringFunc(prompt, func(m string) string {
		sub := mentionRe.FindStringSubmatch(m)
		label, nodeID := sub[1], sub[2]
		for i, id := range idsByLabel[label] {
			if id == nodeID {
				return fmt.Sprintf("%s%d", label, i+1)
			}
		}
		return ""
	})
}
