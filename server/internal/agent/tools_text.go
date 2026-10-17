// Package agent — tools_text.go：内置的 create_text_node 工具，让 LLM 能把
// 剧本/文案这类长文本产物落到画布上，以文本节点的形式呈现。
// 没有它，编剧类 skill 的产出只能整段堆在聊天气泡里——聊天是流式的、不可编辑、
// 不参与画布工作流；文字节点才是后续「@ 引用 / 改写 / 接生成」的锚点。
//
// 与生图/生视频工具不同，文本产物没有对象存储 URL：正文直接放在 Artifact.Text
// 里随 tool_event 帧下发并落库，前端据此在画布上新建一个文字节点（type=text，
// 正文写 settings.text——与用户手建的文字节点同构，画布侧零新增类型）。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// CreateTextNodeToolName 是 LLM 调用该工具时用的标准名字。
const CreateTextNodeToolName = "create_text_node"

// maxTextNodeRunes 给单个文本节点的正文设上限。画布快照整体走 workflow_snapshots
// JSON，单节点塞几 MB 会拖垮快照读写；剧本/文案在十万字内绰绰有余。超限截断并
// 在返回值里告知 LLM，而不是报错——半个剧本落上画布也比整轮失败强。
const maxTextNodeRunes = 100_000

type CreateTextNodeTool struct{}

func (t *CreateTextNodeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	params := map[string]*schema.ParameterInfo{
		"title": {
			Type:     schema.String,
			Desc:     "Short title shown on the canvas node (e.g. 短片剧本《暴雨单》).",
			Required: true,
		},
		"text": {
			Type:     schema.String,
			Desc:     "Full text content to write into the canvas node (script, outline, copy...). Write the COMPLETE text here; do NOT repeat it in the chat reply.",
			Required: true,
		},
	}
	return &schema.ToolInfo{
		Name:        CreateTextNodeToolName,
		Desc:        "Create a text node on the canvas holding long-form text (scripts, outlines, documents). After calling this, tell the user the text has been written to the canvas instead of repeating it in chat.",
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

func (t *CreateTextNodeTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", CreateTextNodeToolName, err)
	}
	if strings.TrimSpace(args.Text) == "" {
		return "", fmt.Errorf("%s: text is required", CreateTextNodeToolName)
	}
	title := strings.TrimSpace(args.Title)
	if title == "" {
		title = "文本节点"
	}

	text := args.Text
	truncated := false
	if r := []rune(text); len(r) > maxTextNodeRunes {
		text = string(r[:maxTextNodeRunes])
		truncated = true
	}

	EmitFromContext(ctx)(Artifact{
		ArtifactType: "text_info",
		ArtifactID:   uuid.NewString(),
		Name:         title,
		Text:         text,
		Source:       CreateTextNodeToolName,
	})

	// 返回值是给 LLM 看的：确认成功 + 明确要求别在聊天里复读全文（只需告诉用户
	// 「已将剧本正文写入画布，请查阅」）。
	if truncated {
		return fmt.Sprintf("已创建文本节点《%s》（正文超长，截断到 %d 字）。请告知用户内容已写入画布节点，不要在聊天里重复正文。", title, maxTextNodeRunes), nil
	}
	return fmt.Sprintf("已创建文本节点《%s》。请告知用户内容已写入画布节点，不要在聊天里重复正文。", title), nil
}
