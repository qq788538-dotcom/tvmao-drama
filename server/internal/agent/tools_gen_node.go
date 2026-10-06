// Package agent — tools_gen_node.go：内置的 create_gen_node 工具，让 LLM 在画布上
// 铺 AI 绘图 / AI 视频节点并预填提示词，**不提交生成、不扣任何积分**。
//
// 与 DynamicTool（提交 generation 任务并计费）是刻意分开的两条路：本仓铁律是
// 「批量入口只铺节点预填参数，生成必须用户逐节点手动点，绝不自动扣积分」——
// 用户要的往往是"帮我起 5 个镜头的提示词"这种预备工作，铺好节点由用户自己
// 挑模型、点生成。模型也不由本工具指定：模型选择在节点 UI 里做，工具乱填一个
// 无效 modelId 反而会卡住用户。
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

// CreateGenNodeToolName 是 LLM 调用该工具时用的标准名字。
const CreateGenNodeToolName = "create_gen_node"

type CreateGenNodeTool struct{}

func (t *CreateGenNodeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	params := map[string]*schema.ParameterInfo{
		"kind": {
			Type:     schema.String,
			Desc:     "Node kind: \"image\" for an AI image node, \"video\" for an AI video node.",
			Required: true,
		},
		"prompt": {
			Type:     schema.String,
			Desc:     "The generation prompt to prefill into the node. Write the full, ready-to-use prompt. If the prompt text already exists in the conversation (user-provided, your own earlier output, or a tool result), copy the relevant span VERBATIM, character for character — do not summarise, rewrite, shorten or translate it.",
			Required: true,
		},
		"title": {
			Type: schema.String,
			Desc: "Optional short label for the node (e.g. 镜头1·雨夜追逐).",
		},
	}
	return &schema.ToolInfo{
		Name:        CreateGenNodeToolName,
		Desc:        "Create an AI image/video generation node on the canvas with the prompt prefilled, WITHOUT running generation (no credits spent). Use when the user wants prompts prepared for manual review — the user picks the model and clicks generate themselves. Call once per node.",
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

func (t *CreateGenNodeTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Kind   string `json:"kind"`
		Prompt string `json:"prompt"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", CreateGenNodeToolName, err)
	}
	kind := strings.TrimSpace(strings.ToLower(args.Kind))
	if kind != "image" && kind != "video" {
		return "", fmt.Errorf("%s: kind must be \"image\" or \"video\", got %q", CreateGenNodeToolName, args.Kind)
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return "", fmt.Errorf("%s: prompt is required", CreateGenNodeToolName)
	}

	// artifact_type 按 kind 分两个显式类型（沿用 *_info 命名），前端各自落
	// gen-image / gen-video 节点；提示词走 Artifact.Prompt 既有字段。
	artifactType := "gen_image_node_info"
	kindLabel := "AI 绘图"
	if kind == "video" {
		artifactType = "gen_video_node_info"
		kindLabel = "AI 视频"
	}
	title := strings.TrimSpace(args.Title)

	EmitFromContext(ctx)(Artifact{
		ArtifactType: artifactType,
		ArtifactID:   uuid.NewString(),
		Name:         title,
		Prompt:       args.Prompt,
		Source:       CreateGenNodeToolName,
	})

	return fmt.Sprintf("已在画布创建%s节点并预填提示词（未生成）。请告知用户：到节点上自选模型并手动点击生成。", kindLabel), nil
}
