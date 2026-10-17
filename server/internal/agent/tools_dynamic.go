// Package agent — tools_dynamic.go：把 agent 工具调用桥接到 generation.Service，
// 这样注册在 agent_tools DB 表里的工具能驱动真正的上游 model，而不是返回占位结果。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
)

// GenerationService 是 DynamicTool 需要用到的 *generation.Service 的子集。
// 做成 interface 方便测试 stub。
type GenerationService interface {
	Submit(ctx context.Context, in generation.SubmitInput) (*generation.SubmitResult, error)
	Get(ctx context.Context, userID, taskID int64) (*generation.Task, error)
}

// ParameterSpec 是从 agent_tools.params_schema 解析出来的 JSON。
// 对 type="array" 来说，Items 描述元素的形态（递归）；实际中元素类型被限制为标量，
// 因为 OpenAI function-call 的 JSON 在这里没办法把对象嵌套到比顶层更深还有意义，
// 但为了完整性仍支持递归。
type ParameterSpec struct {
	Type     string         `json:"type"` // 'string' | 'integer' | 'number' | 'boolean' | 'array'
	Desc     string         `json:"desc"`
	Required bool           `json:"required"`
	Enum     []string       `json:"enum,omitempty"`
	Items    *ParameterSpec `json:"items,omitempty"` // 当 Type == "array" 时必填
}

const (
	defaultPollInterval = 1 * time.Second
	defaultMaxWait      = 5 * time.Minute
)

// URLSigner 把标准的 `media://<key>` URL 转成浏览器能 fetch 的 signed/public HTTP
// URL，以及反向转换。对齐 http.MediaSigner——做成本地 interface，这样 agent 包不
// 依赖 http 包。
//
// 持久化规则：DB 行存标准的 `media://key` 形态，让 URL 能熬过 24h 的 signed-URL TTL。
// 出口给浏览器时（emit / ListMessages / tool_result）走 Sign，每次生成一个新鲜的
// presigned URL。
type URLSigner interface {
	Sign(s string) string
	Canonicalize(s string) string
}

// DynamicTool 把一个以 generation_tasks 为底的 model 包装成一个 eino tool。
type DynamicTool struct {
	ToolName      string
	Category      string
	ModelID       string
	Description   string
	ParamsSchema  map[string]ParameterSpec
	DefaultParams map[string]any
	GenService    GenerationService
	Signer        URLSigner // 可选；设了之后，输出 URL 在 emit / tool result 前会被 sign

	// 给测试用的调参 hook。
	PollInterval time.Duration
	MaxWait      time.Duration
}

// signURLs 返回一个新 slice，其中每个 URL 都经过 t.Signer 签名（如果设了）。
// 没配 signer 时原样返回输入。
func (t *DynamicTool) signURLs(urls []string) []string {
	if t.Signer == nil {
		return urls
	}
	out := make([]string, len(urls))
	for i, u := range urls {
		out[i] = t.Signer.Sign(u)
	}
	return out
}

func (t *DynamicTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	params := make(map[string]*schema.ParameterInfo, len(t.ParamsSchema))
	for name, spec := range t.ParamsSchema {
		params[name] = buildParameterInfo(spec)
	}
	return &schema.ToolInfo{
		Name: t.ToolName, Desc: t.Description,
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

// buildParameterInfo 递归地把一个 ParameterSpec（我们存在 DB 里的 JSON 形态）转成
// Eino 的 ParameterInfo。默认 Type 是 String，这样缺失或未知的 spec.Type 仍能产出
// 一个可用的 schema。
func buildParameterInfo(spec ParameterSpec) *schema.ParameterInfo {
	dt := schema.String
	switch spec.Type {
	case "integer":
		dt = schema.Integer
	case "number":
		dt = schema.Number
	case "boolean":
		dt = schema.Boolean
	case "array":
		dt = schema.Array
	}
	info := &schema.ParameterInfo{
		Type:     dt,
		Desc:     spec.Desc,
		Required: spec.Required,
		Enum:     spec.Enum,
	}
	if dt == schema.Array {
		// Items 缺省时默认为 array-of-string——覆盖参考 image / video / audio 的
		// URL 列表这个常见场景。
		if spec.Items != nil {
			info.ElemInfo = buildParameterInfo(*spec.Items)
		} else {
			info.ElemInfo = &schema.ParameterInfo{Type: schema.String}
		}
	}
	return info
}

func (t *DynamicTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	userID := UserIDFromContext(ctx)
	projectID := ProjectIDFromContext(ctx)
	if userID == 0 {
		return "", errors.New("dynamicTool: missing userID in context")
	}

	var inputs map[string]any
	// LLM 偶尔会发一个完全没有 arguments（空字符串或只有空白）的 tool_call，
	// 当它只想用 default_params 时就这样。把它当成空对象处理，而不是让这次调用失败。
	trimmed := strings.TrimSpace(argsJSON)
	if trimmed == "" {
		inputs = map[string]any{}
	} else if err := json.Unmarshal([]byte(trimmed), &inputs); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", t.ToolName, err)
	}
	// default_params 垫底，args 覆盖在上面
	if t.DefaultParams != nil {
		merged := make(map[string]any, len(t.DefaultParams)+len(inputs))
		for k, v := range t.DefaultParams {
			merged[k] = v
		}
		for k, v := range inputs {
			merged[k] = v
		}
		inputs = merged
	}

	var projectPtr *int64
	if projectID != 0 {
		projectPtr = &projectID
	}

	submitResult, err := t.GenService.Submit(ctx, generation.SubmitInput{
		UserID: userID, ProjectID: projectPtr, ModelID: t.ModelID, Inputs: inputs,
	})
	if err != nil {
		return "", fmt.Errorf("%s: submit: %w", t.ToolName, err)
	}

	switch submitResult.Status {
	case "succeeded":
		return t.handleSucceeded(ctx, submitResult)
	case "failed":
		return "", fmt.Errorf("%s: task failed", t.ToolName)
	case "processing", "queued":
		return t.pollUntilDone(ctx, userID, submitResult.TaskID)
	default:
		return "", fmt.Errorf("%s: unexpected status %q", t.ToolName, submitResult.Status)
	}
}

func (t *DynamicTool) handleSucceeded(ctx context.Context, sr *generation.SubmitResult) (string, error) {
	urls := t.signURLs(sr.OutputURLs)
	t.emitURLs(ctx, urls)
	return formatToolResult(t.ToolName, sr.TaskID, urls), nil
}

func (t *DynamicTool) pollUntilDone(ctx context.Context, userID, taskID int64) (string, error) {
	pollInterval := t.PollInterval
	if pollInterval == 0 {
		pollInterval = defaultPollInterval
	}
	maxWait := t.MaxWait
	if maxWait == 0 {
		maxWait = defaultMaxWait
	}
	deadline := time.Now().Add(maxWait)

	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("%s: timed out after %s", t.ToolName, maxWait)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
		}
		task, err := t.GenService.Get(ctx, userID, taskID)
		if err != nil {
			continue // 瞬时错误——继续轮询
		}
		switch task.Status {
		case "succeeded":
			urls := t.signURLs(extractURLs(task.Output))
			t.emitURLs(ctx, urls)
			return formatToolResult(t.ToolName, taskID, urls), nil
		case "failed":
			return "", fmt.Errorf("%s: task %d failed", t.ToolName, taskID)
		}
	}
}

func (t *DynamicTool) emitURLs(ctx context.Context, urls []string) {
	emit := EmitFromContext(ctx)
	artifactType := "image_info"
	switch t.Category {
	case "video":
		artifactType = "video_info"
	case "audio":
		// 配音/音乐类模型的产物。曾经没有这个分支，音频产物会被打成 image_info、
		// 前端整条链当图片处理（<img> 渲染音频 URL 直接破图）。
		artifactType = "audio_info"
	}
	for _, u := range urls {
		emit(Artifact{
			ArtifactType: artifactType,
			ArtifactID:   uuid.NewString(),
			URL:          u,
			Source:       t.ToolName,
		})
	}
}

func formatToolResult(toolName string, taskID int64, urls []string) string {
	if len(urls) == 0 {
		return fmt.Sprintf("%s succeeded (task=%d) but produced no outputs.", toolName, taskID)
	}
	if len(urls) == 1 {
		return fmt.Sprintf("%s succeeded — URL %s", toolName, urls[0])
	}
	return fmt.Sprintf("%s succeeded — %d outputs: %v", toolName, len(urls), urls)
}

func extractURLs(output json.RawMessage) []string {
	if len(output) == 0 {
		return nil
	}
	var holder struct {
		OutputURLs []string `json:"outputUrls"`
		URLs       []string `json:"urls"`
		URL        string   `json:"url"`
	}
	_ = json.Unmarshal(output, &holder)
	if len(holder.OutputURLs) > 0 {
		return holder.OutputURLs
	}
	if len(holder.URLs) > 0 {
		return holder.URLs
	}
	if holder.URL != "" {
		return []string{holder.URL}
	}
	return nil
}
