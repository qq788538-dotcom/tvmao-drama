package canvasgen

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/canvasstate"
	"github.com/james/huabu_studio/apps/api/internal/generation"
)

// canvasUpstreamTTL 是上游转存前原始地址的时效——「限时去字幕」直连窗口，与
// generation.SinkResult.UpstreamURLs 的 22h 时效对齐（见该字段注释）。
const canvasUpstreamTTL = 22 * time.Hour

// 画布 AI 生成的队列薄层（v5 批次二重构后）。
//
// 上一版在 generation 之上又铺了一张 canvas_gen_tasks 表：自己管状态机、自己起
// retry worker、Submit 后回写 gen_task_id——那次回写就是「恢复逻辑重发 → 双冻结」
// 的崩溃窗口（v5 §3.4）。现在这一层删干净：
//   - 排队/执行/重试/恢复 全部由 generation 队列承担（任务活在 generation_tasks 里）；
//   - 前端拿到的 taskId 就是 generation task id，GetStatus 直查 generation；
//   - 结果经 sink {kind:canvas_node,projectId,nodeId} 直指画布节点，无中间表解引用。

// genClient 是 canvasgen 需要的 generation 子集(窄接口)。*generation.Service 满足它。
type genClient interface {
	Submit(ctx context.Context, in generation.SubmitInput) (*generation.SubmitResult, error)
	Get(ctx context.Context, userID, taskID int64) (*generation.Task, error)
	LatestByCanvasNode(ctx context.Context, userID, projectID int64, nodeID string) (*generation.Task, error)
}

// snapshotMutator 是 projects repo 的窄子集,用于后端权威回写画布节点。
// 返回值是写入后的 version——回写完要把它广播给团队观看端(见 canvasChangeNotifier)。
type snapshotMutator interface {
	MutateSnapshot(projectID int64, fn func(json.RawMessage) (json.RawMessage, bool, error)) (int64, error)
}

// canvasChangeNotifier 是 agent Hub 的窄子集：一次后端回写完成后通知该项目下所有
// 在线连接（在线端据此本地合并这笔变更）。nil = 未装配（单测/未 wire），直接跳过。
type canvasChangeNotifier interface {
	NotifyCanvasChanged(userID, projectID, version int64, sourceMark string, op *canvasstate.CanvasOpSummary)
}

// mediaSigner 是 http.MediaSigner 的窄子集（canvasgen 被 http 依赖，反过来 import
// 会成环）：只要「把一段 JSON 里的媒体引用换成签名 URL」这一件事。
type mediaSigner interface {
	SignJSON(raw json.RawMessage) json.RawMessage
}

type Service struct {
	gen   genClient
	snaps snapshotMutator
	// now 只为测试可注入固定时间(history 条目盖时间戳、seedance upstreamExpire);生产恒为 time.Now。
	now func() time.Time
	// settingsGet 读绘图提示词预设配置（main.go 经 SetSettingsGetter 注入 settings 表的 Get）。
	// nil = 预设能力未装配：presetKey 原样透传、不做图片数校验，执行层的 resolver 兜底。
	settingsGet func(key string) (string, error)
	// notifier 推「画布变了」帧；nil = 未装配，回写照常，只是没人被通知。
	notifier canvasChangeNotifier
	// signer 只给出帧的 op 摘要签名（见 SetMediaSigner）；nil = 不签。
	signer mediaSigner
	// search 按内容找快照(见 SetSnapshotSearcher);nil = 补搬时不改画布副本。
	search snapshotSearcher
}

func NewService(gen genClient, snaps snapshotMutator) *Service {
	return &Service{gen: gen, snaps: snaps, now: time.Now}
}

// SetSettingsGetter 注入 settings 读取器（setter 不动 NewService 签名，同 generation 的先例）。
func (s *Service) SetSettingsGetter(get func(key string) (string, error)) { s.settingsGet = get }

// SetChangeNotifier 注入变更广播器（同上，用 setter 而非改构造签名：canvasgen 与
// agent Hub 之间是可选的单向依赖，没装配也必须能正常出图）。
func (s *Service) SetChangeNotifier(n canvasChangeNotifier) { s.notifier = n }

// SetMediaSigner 注入媒体签名器，只用于**出帧**：广播的 op 摘要里带整节点，而节点
// 里的 content 是 canonical 的 `media://<key>`，浏览器不认这个协议。落库的字节仍是
// canonical，签名只作用于发出去的那份副本。nil = 未装配（单测/未 wire）：帧里保持
// canonical，接收端最差退回整拉快照（那条路上有签名）。
func (s *Service) SetMediaSigner(signer mediaSigner) { s.signer = signer }

// sinkKindCanvasNode 是本模块在 generation sink 注册表里的 kind。
// main.go 用同一个常量注册 handler，避免两处字符串写歪。
const sinkKindCanvasNode = "canvas_node"

// EnqueueInput 是一次画布生成的入队参数。
type EnqueueInput struct {
	UserID    int64
	ProjectID int64
	NodeID    string
	ModelID   string
	Inputs    map[string]any // 含 prompt/尺寸/参考图等,原样传给 generation
	// PresetKey 绘图提示词预设 key（gen-image 专用，空 = 未选）。只透传 key：
	// 正文对前端保密，拼接收口在 generation 执行层（见 SubmitInput.PresetKey）。
	PresetKey string
	// PresetParams 预设参数选中档位（维度 key → 档位 key）。同 PresetKey 只传 key。
	PresetParams map[string]string
}

// canvasSinkRef 是 sink 的持久化形状：直指最终落点（快照里的节点），
// 结果送达时不需要任何中间表解引用。
type canvasSinkRef struct {
	Kind      string `json:"kind"`
	ProjectID int64  `json:"projectId"`
	NodeID    string `json:"nodeId"`
	// UserID 是任务归属账号（团队项目下是影子账号），也就是 agent Hub 的连接键。
	// 存在 sink ref 里是因为回写发生在 worker 里、没有请求上下文，而 generation
	// 的 SinkResult 不带归属；把它编进本模块自己的 ref 是改动面最小的一条路。
	// 升级前入队的存量任务没有这个字段 → 0 → 回写时跳过广播（不影响写入本身）。
	UserID int64 `json:"userId,omitempty"`
}

// Enqueue 提交一次画布生成，返回 generation task id（前端据此轮询 GetStatus）。
// sink 任务入队即返回，不阻塞 handler；错误（余额不足/data URI 等）同步暴露给调用方。
func (s *Service) Enqueue(ctx context.Context, in EnqueueInput) (int64, error) {
	// 预设校验收口在这——网页 /canvas-tasks 与 CLI RunNode 两条提交路的汇合点，
	// 图片数硬约束只写这一遍（网页端按钮禁用只是体验层，CLI 绕不过这道）。
	presetKey := in.PresetKey
	// 多角度/打光保留 key 先分流：它们不在绘图预设表里，走下面的查表会被当悬空
	// key 剥掉。两者的参数都在 resolver 侧按「非法=跳过」容错，这里无须再校验。
	if presetKey != "" && presetKey != MultiAnglePresetKey && presetKey != LightControlPresetKey && s.settingsGet != nil {
		cfg, _ := LoadGenImagePresetsConfig(s.settingsGet) // 配置损坏按未配置处理，不拦生成
		p, ok := cfg.Preset(presetKey)
		if !ok {
			// 后台已删该预设：按未选处理并且不落快照——让任务记录也别留悬空 key。
			presetKey = ""
		} else if msg := presetImageCountViolation(p, imageInputCount(in.Inputs)); msg != "" {
			return 0, &PresetConstraintError{Msg: msg}
		}
	}
	sink, _ := json.Marshal(canvasSinkRef{Kind: sinkKindCanvasNode, ProjectID: in.ProjectID, NodeID: in.NodeID, UserID: in.UserID})
	pid := in.ProjectID
	res, err := s.gen.Submit(ctx, generation.SubmitInput{
		UserID:    in.UserID,
		ProjectID: &pid,
		ModelID:   in.ModelID,
		Inputs:    in.Inputs,
		Sink:      sink,
		PresetKey: presetKey,
		PresetParams: func() map[string]string {
			if presetKey == "" {
				return nil // 预设都没了(被后台删),悬空参数不落快照
			}
			return in.PresetParams
		}(),
	})
	if err != nil {
		return 0, err
	}
	return res.TaskID, nil
}

// OnGenerationResult 是注册给 generation 的 sink handler：任务终态时被调用。
// 只做幂等业务写入并如实返错（v5 §5.7）：
//   - 目标节点已被用户删除 = 成功 no-op（无处可写不是故障）；
//   - 快照损坏 / DB 错 = 真失败，返回 error 让投递方重试；
//   - 同 URL 重写同节点天然幂等，重投安全。
func (s *Service) OnGenerationResult(_ context.Context, ref json.RawMessage, result generation.SinkResult) error {
	var payload canvasSinkRef
	if err := json.Unmarshal(ref, &payload); err != nil || payload.ProjectID == 0 || payload.NodeID == "" {
		return fmt.Errorf("canvasgen sink ref 无效: %s", string(ref))
	}
	if result.Err != "" {
		return s.writeNode(payload.UserID, payload.ProjectID, payload.NodeID, func(node map[string]any) {
			node["status"] = "failed"
			delete(node, "taskId")
			setNodeError(node, result.Err)
		})
	}
	// 文本产物（文本节点的 AI 生成）：写 settings.text 而非 content——content 在
	// 画布语义里是「媒体 URL」，文本节点的正文字段以浏览器 TextNode.tsx 为准。
	// 不并 genHistory：历史条目形状是 {url,...} 且与前端 genHistory.ts 逐字同构，
	// 文本硬塞进去要两侧改形状（拍板：第一版不做文本历史）。
	if len(result.OutputURLs) == 0 && result.Text != "" {
		return s.writeNode(payload.UserID, payload.ProjectID, payload.NodeID, func(node map[string]any) {
			settings := nodeSettings(node)
			settings["text"] = result.Text
			node["status"] = "succeeded"
			delete(node, "taskId")
			clearNodeError(node)
		})
	}
	if len(result.OutputURLs) == 0 {
		return s.writeNode(payload.UserID, payload.ProjectID, payload.NodeID, func(node map[string]any) {
			node["status"] = "failed"
			delete(node, "taskId")
			setNodeError(node, "上游未返回结果")
		})
	}
	url := result.OutputURLs[0]
	at := s.now().UTC().Format(time.RFC3339)
	return s.writeNode(payload.UserID, payload.ProjectID, payload.NodeID, func(node map[string]any) {
		// 先并入 history(它要读改写前的 content 作兜底),再改 content。
		// assetId/kind 同理由后端补:节点靠 assetId 做「保存到素材库」和合规校验,
		// 只靠前端写的话,用户关着页面出的图就永远没有 asset 身份。
		appendHistory(node, url, at)
		settings := nodeSettings(node)
		if len(result.AssetIDs) > 0 {
			settings["assetId"] = result.AssetIDs[0]
		}
		if kind := nodeMediaKind(node); kind != "" {
			settings["kind"] = kind
		}
		node["content"] = url
		node["status"] = "succeeded"
		delete(node, "taskId")
		clearNodeError(node)
		// seedance 视频:额外存 22h 上游地址的过期时刻 + 源任务 id,供「限时去字幕」反查。
		// 裸 upstream url 不写节点(留在 generation_tasks.output.upstreamUrls);节点只存无害的
		// expire 时间戳(前端判限时项显隐)和 srcGenTaskId(后端反查)。
		if strings.HasPrefix(result.ModelID, "doubao-seedance") && len(result.UpstreamURLs) > 0 {
			settings, ok := node["settings"].(map[string]any)
			if !ok {
				settings = map[string]any{}
				node["settings"] = settings
			}
			settings["upstreamExpire"] = s.now().Add(canvasUpstreamTTL).Format(time.RFC3339)
			settings["srcGenTaskId"] = result.TaskID
		}
	})
}

// Status 是前端轮询画布任务的结果。形状与上层表时代逐字一致，前端不改。
type Status struct {
	Status     string   `json:"status"` // processing / succeeded / failed
	OutputURLs []string `json:"outputUrls,omitempty"`
	AssetIDs   []int64  `json:"assetIds,omitempty"` // 与 outputUrls 同序;节点靠它做「保存素材库」「合规校验」
	Text       string   `json:"text,omitempty"`     // 文本生成任务的产物;与 outputUrls 互斥
	Error      string   `json:"error,omitempty"`
}

// GetStatus 供前端轮询：taskID 即 generation task id。
// queued/running/processing/retry_pending 一律映射 processing——排队、执行、重试对
// 前端都是「生成中」，重试细节画布不展示（v5 §4.4：画布无「重试中(n)」消费方）。
func (s *Service) GetStatus(ctx context.Context, userID, taskID int64) (*Status, error) {
	t, err := s.gen.Get(ctx, userID, taskID)
	if err != nil {
		return nil, err
	}
	return statusFromTask(t), nil
}

func statusFromTask(t *generation.Task) *Status {
	switch t.Status {
	case "succeeded":
		return &Status{
			Status:     "succeeded",
			OutputURLs: extractOutputURLs(t.Output),
			AssetIDs:   extractAssetIDs(t.Output),
			Text:       extractOutputText(t.Output),
		}
	case "failed":
		return &Status{Status: "failed", Error: t.Error}
	default:
		return &Status{Status: "processing"}
	}
}

// NodeTaskStatus 是「按节点找回任务」的返回：状态之外必须带 taskId——
// 调用场景是前端节点丢了 taskId,找回后交还现有轮询,光有状态无法续接。
type NodeTaskStatus struct {
	TaskID int64 `json:"taskId"`
	Status
}

// GetNodeStatus 按 (user, project, nodeId) 找该画布节点最新一次任务。
// 无任务时透传 generation.ErrNotFound,由 handler 映射 404——「查无」对前端
// 是有效信号(连续查无若干轮才判死节点),不能和服务器故障混在一个错误码里。
func (s *Service) GetNodeStatus(ctx context.Context, userID, projectID int64, nodeID string) (*NodeTaskStatus, error) {
	t, err := s.gen.LatestByCanvasNode(ctx, userID, projectID, nodeID)
	if err != nil {
		return nil, err
	}
	return &NodeTaskStatus{TaskID: t.ID, Status: *statusFromTask(t)}, nil
}

func extractOutputURLs(raw json.RawMessage) []string {
	var m struct {
		OutputURLs []string `json:"outputUrls"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.OutputURLs
}

func extractAssetIDs(raw json.RawMessage) []int64 {
	var m struct {
		AssetIDs []int64 `json:"assetIds"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.AssetIDs
}

func extractOutputText(raw json.RawMessage) string {
	var m struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.Text
}
