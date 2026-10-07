package cliapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
	"github.com/james/huabu_studio/apps/api/internal/canvasinputs"
	"github.com/james/huabu_studio/apps/api/internal/canvasstate"
	"github.com/james/huabu_studio/apps/api/internal/generation"
	"github.com/james/huabu_studio/apps/api/internal/models"
)

// CLI 的节点运行。
//
// 这里是整个 CLI 契约里唯一会花钱的地方，所以三条规则一条都不能省：
//
//  1. **模型必须显式**。节点没有 modelId 就报错，绝不挑一个顶上——生产里 512 个
//     成功节点当初用的哪个模型已不可考，猜出来的结果用户无从察觉。
//  2. **进行中拒绝重复运行**。第一阶段网页与 CLI 之间没有实时同步，用户看不到
//     CLI 的改动就会重复触发，而每次触发都是一次真实的生成。
//  3. **先入队拿 taskID，再权威 patch 节点**。两步不在同一事务；进程在中间挂掉时
//     由幂等记录取回同一 taskID 补做 patch，**禁止用「重新提交一个任务」修复**。

// Enqueuer 是 run 需要的 canvasgen 子集。窄接口便于单测注入假实现。
type Enqueuer interface {
	Enqueue(ctx context.Context, in canvasgen.EnqueueInput) (int64, error)
	GetStatus(ctx context.Context, userID, taskID int64) (*canvasgen.Status, error)
}

// ModelLookup 是 run 需要的模型目录子集。
type ModelLookup interface {
	GetByModelID(modelID string) (*models.ModelConfig, error)
}

// WithGeneration 注入生成依赖。分开设置而不是塞进 NewService，是为了让只读
// 场景（如未来的只读 API）不必构造整条生成链。
func (s *Service) WithGeneration(gen Enqueuer, mdl ModelLookup) *Service {
	s.gen = gen
	s.models = mdl
	return s
}

type RunRequest struct {
	// Overrides 只覆盖本次运行的参数，合并结果会写回节点。
	Overrides map[string]any `json:"overrides,omitempty"`
}

type RunResponse struct {
	TaskID  int64          `json:"taskId"`
	Status  string         `json:"status"`
	ModelID string         `json:"modelId"`
	NodeID  string         `json:"nodeId"`
	Inputs  map[string]any `json:"inputs"`
}

type RunStatus struct {
	TaskID     int64    `json:"taskId"`
	Status     string   `json:"status"` // processing | succeeded | failed
	OutputURLs []string `json:"outputUrls,omitempty"`
	AssetIDs   []int64  `json:"assetIds,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// RunNode 提交一次生成。
func (s *Service) RunNode(ctx context.Context, userID, projectID int64, nodeID string, req RunRequest) (RunResponse, error) {
	if s.gen == nil || s.models == nil {
		return RunResponse{}, New(CodeInternal, "生成能力未配置")
	}
	if _, err := s.project(userID, projectID); err != nil {
		return RunResponse{}, err
	}

	snap, err := s.projects.GetLatest(projectID)
	if err != nil || snap == nil {
		return RunResponse{}, Errf(CodeNodeNotFound, "节点 %s 不存在", nodeID)
	}
	doc, err := decodeDoc(snap.Payload)
	if err != nil {
		return RunResponse{}, err
	}
	raw, ok := FindNode(doc, nodeID)
	if !ok {
		return RunResponse{}, Errf(CodeNodeNotFound, "节点 %s 不存在", nodeID)
	}

	internal := str(raw["type"])
	semantic, writable := SemanticType(internal)
	if !writable {
		return RunResponse{}, Errf(CodeNodeReadonlyType, "节点 %s 的类型 %q 只读，不能运行", nodeID, semantic)
	}
	if internal != "gen-image" && internal != "gen-video" {
		return RunResponse{}, Errf(CodeInvalidNodeType, "节点 %s 的类型 %q 不是生成节点", nodeID, semantic)
	}
	// 重复运行保护。没有它，用户在网页上看不到 CLI 的改动时会重复触发，
	// 每一次都是一次真实的生成（spec §9.7）。
	if str(raw["status"]) == "generating" {
		return RunResponse{}, Errf(CodeNodeAlreadyRunning,
			"节点 %s 正在生成中，请等它结束或用 tvmao run wait 跟踪", nodeID)
	}

	// effective 一律用 **CLI 参数名**（gen-video 的提示词在这里叫 prompt，
	// 尽管画布里存的是 videoPrompt）——这样 req.Overrides 能直接覆盖上去，
	// 下面取 prompt 的那行也不用分类型写两遍。写回快照前再翻译回去。
	settings, _ := raw["settings"].(map[string]any)
	effective := paramsFromSettings(internal, settings)
	if err := validateParams(req.Overrides); err != nil {
		return RunResponse{}, err
	}
	for k, v := range req.Overrides {
		effective[k] = v
	}

	modelID := strings.TrimSpace(strOf(effective["modelId"]))
	if modelID == "" {
		// 生产里 52% 的成功 gen-image 节点没存 modelId。当初用的哪个模型数据上
		// 已不可考——只能推断为「当时的 models[0]」，而这个顺序会随后台调整
		// sort_order 而改变。猜一个的话，用户拿到的结果可能与记忆中那次完全不同
		// 且无从察觉。宁可报错。
		return RunResponse{}, New(CodeModelRequired,
			"该节点没有保存模型，请用 --model 显式指定（服务端不会替你挑一个）")
	}
	model, err := s.models.GetByModelID(modelID)
	if err != nil || model == nil {
		return RunResponse{}, Errf(CodeModelNotFound, "模型 %q 不存在", modelID)
	}
	if !model.Enabled {
		return RunResponse{}, Errf(CodeModelDisabled, "模型 %q 已被停用，请用 tvmao model list 换一个", modelID)
	}
	// 模型类型必须与节点类型匹配。不匹配时静默换模型比报错危险得多——
	// 用户会拿到一个不知道从哪来的产物。
	wantKind := map[string]string{"gen-image": "image", "gen-video": "video"}[internal]
	if model.Type != wantKind {
		return RunResponse{}, Errf(CodeInvalidModelParams,
			"节点类型 %q 需要 %s 模型，%q 是 %s 模型", semantic, wantKind, modelID, model.Type)
	}

	// 三步派生：与浏览器逐字同构（canvasinputs 包注释）。
	cs, err := canvasinputs.ParseSnapshot(snap.Payload)
	if err != nil {
		return RunResponse{}, New(CodeInternal, "画布快照损坏").WithCause(err)
	}
	linked := canvasinputs.Connected(cs, nodeID, s.sign)
	dynamic := canvasinputs.BuildDynamicInputs(model.UISchema, effective)
	prompt := strOf(effective["prompt"])
	fallback := strings.Join(linked.Texts, "\n")

	var inputs map[string]any
	if internal == "gen-image" {
		// @[图片:节点ID] → 「图片N」。网页在提交瞬间做这层翻译（GenImageNode 的
		// serializeMentionPrompt），CLI 提交也必须做，否则 token 原样进上游 prompt。
		// gen-image 只认图片引用（网页 refs.filter(kind==='image')），视频/音频
		// token 走死引用剔除。
		prompt = canvasinputs.SerializeMentionPrompt(prompt, linked.ImageIDs, nil, nil)
		inputs = canvasinputs.BuildImageInputs(prompt, fallback, dynamic, linked.Images)
	} else {
		prompt = canvasinputs.SerializeMentionPrompt(prompt, linked.ImageIDs, linked.VideoIDs, linked.AudioIDs)
		inputs = canvasinputs.BuildVideoInputs(prompt, fallback, dynamic, linked.Images, linked.Videos, linked.Audios)
	}

	// 绘图提示词预设：CLI 不暴露该参数，节点 settings.promptPreset（网页写入的 key）
	// 是唯一来源——保证同一节点 CLI 提交与网页提交走到同一个预设。只 gen-image 认：
	// 预设是绘图节点专属（gen_image_prompt_presets）。
	presetKey := ""
	var presetParams map[string]string
	if internal == "gen-image" {
		presetKey = strOf(settings["promptPreset"])
		// presetParams 同 promptPreset：网页写入节点 settings，CLI 只透传 key。
		// 快照 JSON 反序列化后是 map[string]any，逐项取 string（非 string 丢弃）。
		if raw, ok := settings["presetParams"].(map[string]any); ok {
			for k, v := range raw {
				if sv, ok := v.(string); ok && sv != "" {
					if presetParams == nil {
						presetParams = map[string]string{}
					}
					presetParams[k] = sv
				}
			}
		}
	}

	// 先入队。入队失败时节点状态一动未动，用户重试是干净的。
	taskID, err := s.gen.Enqueue(ctx, canvasgen.EnqueueInput{
		UserID:       userID,
		ProjectID:    projectID,
		NodeID:       nodeID,
		ModelID:      modelID,
		Inputs:       inputs,
		PresetKey:    presetKey,
		PresetParams: presetParams,
	})
	if err != nil {
		// 存储已满：用户清理素材可自解，归 INVALID_ARGUMENT（不可重试）——
		// 落 UPSTREAM_UNAVAILABLE 会被 CLI 当瞬时故障重试，重试改变不了配额。
		if errors.Is(err, generation.ErrStorageQuotaExceeded) {
			return RunResponse{}, New(CodeInvalidArgument, "存储空间已满，请清理后重试再生成")
		}
		// 输入数量违约归 INVALID_ARGUMENT（不可重试）——落 UPSTREAM_UNAVAILABLE
		// 会被 CLI 自动重试，重试改变不了画布连线。
		var limitErr *generation.InputLimitError
		if errors.As(err, &limitErr) {
			return RunResponse{}, New(CodeInvalidArgument, limitErr.Error())
		}
		// 输入视频时长不可读同上：换视频/重传可自解，不可重试，归 INVALID_ARGUMENT
		// 而非会被 CLI 自动重试的 UPSTREAM_UNAVAILABLE。
		var durErr *generation.InputDurationError
		if errors.As(err, &durErr) {
			return RunResponse{}, New(CodeInvalidArgument, durErr.Error())
		}
		// 预设的图片数约束不满足：改连线可自解，不可重试，同队 INVALID_ARGUMENT。
		var presetErr *canvasgen.PresetConstraintError
		if errors.As(err, &presetErr) {
			return RunResponse{}, New(CodeInvalidArgument, presetErr.Error())
		}
		return RunResponse{}, New(CodeUpstreamUnavailable, "提交生成失败："+err.Error()).WithCause(err)
	}

	// 再权威 patch 节点。这两步不在同一事务：进程若在中间退出，重试时幂等记录
	// 会取回同一个 taskID 并补做这次 patch；即使补写始终没发生，最终 sink 仍能把
	// 任务终态写回目标节点。**不要用「重新提交一个任务」来修这个窗口**。
	patch := map[string]any{
		"status": "generating",
		"taskId": taskID,
		// effective 用的是 CLI 参数名，写回快照前必须翻回 settings 键名，
		// 否则 gen-video 会在 videoPrompt 旁边多写一个野生的 prompt。
		"settings": mergeSettingsPatch(internal, effective, map[string]any{
			// pendingGen 与网页同构：这次提交的词与参数当场落库，等结果回来时
			// 由 sink 写进 history 条目。不能等那时再读——用户边等边改词是常态。
			"pendingGen": map[string]any{"prompt": inputs["prompt"], "params": dynamic},
			"error":      nil,
		}),
	}
	patchJSON, mErr := json.Marshal(patch)
	if mErr == nil {
		_, _ = s.projects.MutateSnapshot(projectID, func(payload json.RawMessage) (json.RawMessage, bool, error) {
			next, changed, _, err := canvasstate.PatchNode(ensurePayload(payload), nodeID, patchJSON)
			return next, changed, err
		})
	}

	return RunResponse{
		TaskID:  taskID,
		Status:  "processing",
		ModelID: modelID,
		NodeID:  nodeID,
		Inputs:  inputs,
	}, nil
}

// GetRun 查询任务状态。只是 canvasgen.GetStatus 的转译——CLI 的 --wait 轮询这里，
// 不另建一套任务状态机。
func (s *Service) GetRun(ctx context.Context, userID, taskID int64) (RunStatus, error) {
	if s.gen == nil {
		return RunStatus{}, New(CodeInternal, "生成能力未配置")
	}
	st, err := s.gen.GetStatus(ctx, userID, taskID)
	if err != nil || st == nil {
		// generation 的 Get 会校验归属；查不到与不属于自己都落这里，
		// 一律 404（不区分，避免 task id 可枚举）。
		return RunStatus{}, Errf(CodeNodeNotFound, "任务 %d 不存在", taskID)
	}
	out := RunStatus{TaskID: taskID, Status: st.Status, AssetIDs: st.AssetIDs, Error: st.Error}
	for _, u := range st.OutputURLs {
		out.OutputURLs = append(out.OutputURLs, s.sign(u))
	}
	return out, nil
}

// mergeSettingsPatch 把生效参数与附加键合成一个 settings merge patch。
// effective 里是 CLI 参数名，这里翻成画布 settings 键名；extra 已经是
// settings 键名（pendingGen / error 都是画布内部约定），不再过翻译。
func mergeSettingsPatch(internalType string, effective map[string]any, extra map[string]any) map[string]any {
	out := settingsFromParams(internalType, effective)
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func strOf(v any) string {
	s, _ := v.(string)
	return s
}
