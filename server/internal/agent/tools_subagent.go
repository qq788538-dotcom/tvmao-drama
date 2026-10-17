// Package agent — tools_subagent.go：内置的 vision / video 分析工具。planner 调用
// 它们，把「读这张图」或「概括这个视频」委派给配置好的 subagent_vision /
// subagent_video backbone。当对应的 subagent 角色没配置时，两者都回退到 planner
// backbone（见 BuildSubagent*ForUser）。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	neturl "net/url"
	"path"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/billing"
)

const (
	AnalyzeImageToolName = "analyze_image"
	AnalyzeVideoToolName = "analyze_video"
	AnalyzeAudioToolName = "analyze_audio"
)

// SubagentModelBuilder 为某个 subagent 角色构造 eino model，并连同它的计费配置
// 一起返回——subagent 的视觉/视频分析也是一次真实 LLM 往返，必须像 planner /
// summarizer 一样 freeze→settle，所以 mb 不能再被丢弃。做成注入式，这样 registry
// 能接生产实现（BuildSubagent*ForUser），测试能 stub。
type SubagentModelBuilder func(ctx context.Context, userID int64, deps PlannerDeps) (model.ChatModel, billing.ModelBilling, error)

type analyzeMediaTool struct {
	name    string
	desc    string
	urlDesc string
	// kind 决定媒体以哪种部件类型发给 backbone："image" → image_url、
	// "video" → video_url、"audio" → audio_url。部件类型必须和媒体实际类型一致：
	// 曾经视频也装在 image_url 里发出，上游按图片解码 mp4 直接失败（用户看到
	// 「视频解析接口未能读取这条链接」），配对了视频模型也救不回来。
	kind  string
	deps  PlannerDeps
	build SubagentModelBuilder
}

// mediaPart 按 kind 构造这次分析请求的媒体部件。
func (t *analyzeMediaTool) mediaPart(url string) schema.ChatMessagePart {
	switch t.kind {
	case "video":
		return schema.ChatMessagePart{
			Type:     schema.ChatMessagePartTypeVideoURL,
			VideoURL: &schema.ChatMessageVideoURL{URL: url},
		}
	case "audio":
		return schema.ChatMessagePart{
			Type: schema.ChatMessagePartTypeAudioURL,
			// MIMEType 会被 openai 适配层转成 input_audio.format（"wav"/"mp3" 这类
			// 短格式），从 URL 扩展名推断。
			AudioURL: &schema.ChatMessageAudioURL{URL: url, MIMEType: audioFormatFromURL(url)},
		}
	default:
		return schema.ChatMessagePart{
			Type:     schema.ChatMessagePartTypeImageURL,
			ImageURL: &schema.ChatMessageImageURL{URL: url},
		}
	}
}

// audioFormatFromURL 从 URL 路径的扩展名推断音频格式，返回 OpenAI 兼容协议
// input_audio.format 的短格式取值（"mp3"/"wav"/…）。签名 URL 的 query 不参与；
// 解析失败或没有扩展名时默认 "mp3"——上游多数对 format 容错，缺省比报错强。
func audioFormatFromURL(u string) string {
	parsed, err := neturl.Parse(u)
	if err != nil {
		return "mp3"
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(parsed.Path), "."))
	switch ext {
	case "mp3", "wav", "m4a", "aac", "ogg", "flac", "opus":
		return ext
	default:
		return "mp3"
	}
}

func (t *analyzeMediaTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.name,
		Desc: t.desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"url": {
				Type:     schema.String,
				Desc:     t.urlDesc,
				Required: true,
			},
			"question": {
				Type:     schema.String,
				Desc:     "What you want to know about the media. Be specific (e.g. 'describe the main subject and dominant colours').",
				Required: true,
			},
		}),
	}, nil
}

func (t *analyzeMediaTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		URL      string `json:"url"`
		Question string `json:"question"`
	}
	trimmed := strings.TrimSpace(argsJSON)
	if trimmed == "" {
		return "", fmt.Errorf("%s: empty args (url + question required)", t.name)
	}
	if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", t.name, err)
	}
	if args.URL == "" {
		return "", fmt.Errorf("%s: url is required", t.name)
	}
	if args.Question == "" {
		return "", fmt.Errorf("%s: question is required", t.name)
	}

	userID := UserIDFromContext(ctx)
	if userID == 0 {
		return "", fmt.Errorf("%s: missing user_id in context", t.name)
	}

	// S12 接缝：本机媒体换成上游拿得到的形式；视频/音频目前给不出，直接把原因告诉 planner，
	// 不去冻结、不调上游（调了只会拿到一个看不懂的 400）。
	if t.deps.Media != nil {
		resolved, rerr := t.deps.Media.ResolveForLLM(ctx, t.kind, args.URL)
		if rerr != nil {
			return "", fmt.Errorf("%s: %w", t.name, rerr)
		}
		args.URL = resolved
	}

	// build 同时回 backbone 的计费配置。匹配不到 model_configs 时 buildModelForUser
	// 已经 error → 工具调用失败（符合「匹配不到拒绝」，不允许免费白嫖）。
	cm, mb, err := t.build(ctx, userID, t.deps)
	if err != nil {
		return "", fmt.Errorf("%s: build model: %w", t.name, err)
	}

	// freeze 这次 subagent LLM 往返。余额不足/冻结失败 → 让本次工具调用失败。
	// Billing 为 nil（单测 stub）时跳过计费，freezeAmt 仍算出但 freezeDone=false。
	freezeAmt := billing.EstimateFreeze(mb, nil)
	freezeDone := false
	if t.deps.Billing != nil {
		if _, ferr := t.deps.Billing.Freeze(ctx, userID, agentNoTask, freezeAmt, "AI 助手 · 多媒体分析"); ferr != nil {
			return "", fmt.Errorf("%s: freeze: %w", t.name, ferr)
		}
		freezeDone = true
	}

	msg := &schema.Message{
		Role: schema.User,
		MultiContent: []schema.ChatMessagePart{
			{Type: schema.ChatMessagePartTypeText, Text: args.Question},
			t.mediaPart(args.URL),
		},
	}
	out, err := cm.Generate(ctx, []*schema.Message{msg})
	if err != nil {
		// freeze 后失败 → 退款（资金安全：freeze 后每条出口必配对）。
		if freezeDone {
			chargeRefund(ctx, t.deps.Billing, userID, freezeAmt, "AI 助手 · 多媒体分析")
		}
		return "", fmt.Errorf("%s: generate: %w", t.name, err)
	}
	if out == nil {
		if freezeDone {
			chargeRefund(ctx, t.deps.Billing, userID, freezeAmt, "AI 助手 · 多媒体分析")
		}
		return "", fmt.Errorf("%s: empty response", t.name)
	}

	// 成功 → settle。从 out.ResponseMeta.Usage 拼 response map 复用 token 计费；
	// 拿不到 usage 按冻结额结算，不退不补。
	if freezeDone {
		chargeSettle(ctx, t.deps.Billing, userID, freezeAmt, mb, usageFromMessage(out), "AI 助手 · 多媒体分析")
	}
	return out.Content, nil
}

// NewAnalyzeImageTool 返回内置的 analyze_image 工具。builder 为 nil 时默认用
// BuildSubagentVisionForUser。
func NewAnalyzeImageTool(deps PlannerDeps, builder SubagentModelBuilder) Tool {
	if builder == nil {
		builder = BuildSubagentVisionForUser
	}
	return &analyzeMediaTool{
		name:    AnalyzeImageToolName,
		desc:    "Inspect an image and answer a question about it (composition, subject, text, colours, defects, etc.). Use this when the user references an image and you need to see/understand its content before responding or before generating a new image.",
		urlDesc: "Public URL of the image to inspect.",
		kind:    "image",
		deps:    deps,
		build:   builder,
	}
}

// NewAnalyzeVideoTool 返回内置的 analyze_video 工具。builder 为 nil 时默认用
// BuildSubagentVideoForUser。
func NewAnalyzeVideoTool(deps PlannerDeps, builder SubagentModelBuilder) Tool {
	if builder == nil {
		builder = BuildSubagentVideoForUser
	}
	return &analyzeMediaTool{
		name:    AnalyzeVideoToolName,
		desc:    "Inspect a video and answer a question about its content (scenes, actions, dialogue, motion, defects, etc.). Use this when the user references a video and you need to understand it before responding or before generating new content.",
		urlDesc: "Public URL of the video to inspect.",
		kind:    "video",
		deps:    deps,
		build:   builder,
	}
}

// NewAnalyzeAudioTool 返回内置的 analyze_audio 工具。builder 为 nil 时默认用
// BuildSubagentAudioForUser。
func NewAnalyzeAudioTool(deps PlannerDeps, builder SubagentModelBuilder) Tool {
	if builder == nil {
		builder = BuildSubagentAudioForUser
	}
	return &analyzeMediaTool{
		name:    AnalyzeAudioToolName,
		desc:    "Listen to an audio file and answer a question about its content (speech transcript, music style, mood, sound effects, defects, etc.). Use this when the user references an audio file and you need to understand it before responding.",
		urlDesc: "Public URL of the audio file to inspect.",
		kind:    "audio",
		deps:    deps,
		build:   builder,
	}
}
