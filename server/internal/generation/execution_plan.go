package generation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/assets"

	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/upstream"
)

// executionPlan 是"调一次上游"所需的全部材料。
//
// 抽出来是为了让 Submit 和队列 worker 走同一条准备路径：worker 领到的任务可能是
// 别的实例（甚至上一个版本的进程）提交的，只能从 DB 里的 (modelID, userID, inputs)
// 重新推导出这些材料。两边共用一个函数，就不会出现"提交时和重建时准备得不一样"。
//
// 顺带一个好处：媒体 URL 的签名在这里现签。排队可能等几分钟，提交时签好的 URL 到
// 执行时可能已经过期——重建时重签反而更安全。
type executionPlan struct {
	model            *models.ModelConfig
	apiKey           string
	baseDomain       string
	payload          []byte
	normalizedInputs map[string]any
	preparedImages   []preparedImageInput
	note             string
}

// prepareExecution 从最小输入推导出执行材料。任何一步失败都返回错误，调用方决定
// 是拒绝提交（Submit 路径）还是标记任务失败并退款（worker 路径）。
func (s *Service) prepareExecution(ctx context.Context, userID int64, modelID string, inputs map[string]any, noteLabel, presetKey string, presetParams map[string]string) (*executionPlan, error) {
	model, err := s.models.GetByModelID(modelID)
	if err != nil {
		return nil, fmt.Errorf("load model: %w", err)
	}
	if !model.Enabled {
		return nil, errors.New("model disabled")
	}

	// 绘图提示词预设：key → 正文的替换收口在这里。执行层是「所有会回读给前端的
	// 存储」之后、上游请求之前的唯一位置——GET /tasks 会把 input 快照原样吐回，
	// 拼接结果绝不能早于此出现（对前端保密，设计文档需求第 9 条）。
	// key 查不到（后台已删该预设）静默按未选处理，不让存量任务执行失败。
	// 每次执行现读配置（resolver 内部查 settings）：后台改了预设正文，排队中的
	// 任务执行时自动用新文案，与「节点只存 key」同一语义。
	if presetKey != "" && s.promptPreset != nil {
		if text, ok := s.promptPreset(presetKey, presetParams); ok && text != "" {
			inputs = applyPresetPrompt(inputs, text)
		}
	}

	// 提交边界按 uiSchema 归一化枚举参数的类型——写入方(前端/LLM 分镜提取/回填)对
	// 枚举值类型把握不一，schema 才是真源。
	inputs = normalizeInputsToUISchema(model.UISchema, inputs)

	// doubao-seedance* 要求已通过合规校验的图以 asset://<id> 引用。这一步放在重建里
	// 而不是只在提交时做，是因为校验状态可能在排队期间才刚变成 active。
	inputs, err = s.applyRealPersonRefs(userID, model, inputs)
	if err != nil {
		return nil, err
	}

	apiKey, baseDomain, err := s.upstreamCredentials(userID, model)
	if err != nil {
		return nil, err
	}

	requireURLImageBytes := model.ImageRequestMode == models.ImageRequestModeOpenAIImageEdits
	upstreamInputs, err := s.signInputMediaReferences(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("sign input media references: %w", err)
	}
	normalizedInputs, preparedImages, err := prepareImageInputs(ctx, upstreamInputs, model.ImageInputFormat, requireURLImageBytes, s.fetchInputImage, s.spoolInputImage)
	if err != nil {
		return nil, fmt.Errorf("prepare image inputs: %w", err)
	}

	payload, err := upstream.InterpolateTemplate(model.RequestTemplate, normalizedInputs)
	if err != nil {
		return nil, fmt.Errorf("interpolate template: %w", err)
	}
	// S12 未定：插值后的请求里还留着本机地址，上游访问不到，调上游之前失败。
	if err := s.ensureNoLocalMediaForRequest(model, payload, preparedImages); err != nil {
		removePreparedFiles(preparedImages)
		return nil, err
	}

	return &executionPlan{
		model:            model,
		apiKey:           apiKey,
		baseDomain:       baseDomain,
		payload:          payload,
		normalizedInputs: normalizedInputs,
		preparedImages:   preparedImages,
		note:             ledgerNote(model, noteLabel),
	}, nil
}

// applyPresetPrompt 把预设正文拼到用户 prompt 之前（拷贝一份 map，不改调用方持有的
// 原 inputs——它可能还是任务快照的引用）。用户 prompt 为空时只用预设正文，避免尾随换行。
func applyPresetPrompt(inputs map[string]any, presetText string) map[string]any {
	merged := make(map[string]any, len(inputs))
	for k, v := range inputs {
		merged[k] = v
	}
	if user, _ := merged["prompt"].(string); user != "" {
		merged["prompt"] = presetText + "\n" + user
	} else {
		merged["prompt"] = presetText
	}
	return merged
}

// taskSnapshot 是 task.Input 里存的执行快照，worker 靠它重建执行材料。
type taskSnapshot struct {
	ModelID string         `json:"modelId"`
	Inputs  map[string]any `json:"inputs"`
	Note    string         `json:"note"`
	// PresetKey/PresetParams 见 SubmitInput 同名字段：快照只存 key 不存正文，
	// worker 执行时现解析。
	PresetKey    string            `json:"presetKey,omitempty"`
	PresetParams map[string]string `json:"presetParams,omitempty"`
}

// containsDataURI 深度检查 inputs 里是否有内联的 data URI（"data:...,..."）。
// sink 任务在 Submit 建行前用它拒绝（v5 §3.1）：快照会把 data URI 脱敏，队列 worker
// 重建不出来；产品路径全 URL，出现 data URI 只可能是绕过前端的直连调用方。
func containsDataURI(inputs map[string]any) bool {
	found := false
	assets.WalkStrings(inputs, func(s string) string {
		if strings.HasPrefix(s, "data:") && strings.Contains(s, ",") {
			found = true
		}
		return s
	})
	return found
}
