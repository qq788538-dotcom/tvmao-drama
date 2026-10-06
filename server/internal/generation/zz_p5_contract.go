// 本文件是 P5 的契约桩：P1b/P1c 尚未合并时让 agent 包能编译。内容逐字取自参考仓
// fea92ab 的同名声明。最终合并时，只要该包已有真实实现（go build 报重复声明），就删除本文件。

package generation

import (
	"encoding/json"
	"time"
)

type Task struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"userId"`
	ProjectID *int64 `json:"projectId,omitempty"`
	ModelID   string `json:"modelId"`
	Status    string `json:"status"`
	// OutputKind 产物类型大类(image/video/audio)——0103 上列,List 过滤/回显用;
	// Get/LatestByCanvasNode 不读它(消费方不需要),空串=未知。
	OutputKind     string          `json:"outputKind,omitempty"`
	UpstreamTaskID string          `json:"upstreamTaskId,omitempty"`
	Input          json.RawMessage `json:"input"`
	Output         json.RawMessage `json:"output,omitempty"`
	FrozenCents    int64           `json:"frozenCents"`
	CostCents      *int64          `json:"costCents,omitempty"`
	AuditFlag      string          `json:"auditFlag,omitempty"`
	Error          string          `json:"error,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	DramaProjectID int64           // 0=无；drama 归属载体，finalize 结算时读回重建 ctx scope
	DramaSegmentID int64
	// ActorUserID 团队消费的真人成员(0=个人)。与 Drama* 同一个套路：写入时落列、
	// finishTx 收口时读回重建 billing.WithActor——worker 用 Background ctx，
	// 不落列就丢 actor，额度只扣不回冲(0105)。只写不读进 Get/List(消费方不需要)。
	ActorUserID int64 `json:"-"`

	// ── 持久化队列（0080）────────────────────────────────────────────
	// Sink 结果去向。generation 只认其中的 kind，其余字段原样交给注册的 handler。
	Sink json.RawMessage `json:"-"`
	// UpstreamCalledAt 首次向上游发出请求的时刻。被中断后能否安全重排全靠它：
	// nil=什么都没发生可原样重排；非 nil 但没拿到 UpstreamTaskID=不确定上游收没收，
	// 只能保守退款，绝不重复扣费。
	UpstreamCalledAt *time.Time `json:"-"`
	Attempt          int        `json:"-"` // 已尝试次数（含重排）；不影响计费
	NextRetryAt      *time.Time `json:"-"`
}

type SubmitInput struct {
	UserID    int64           `json:"-"`
	ProjectID *int64          `json:"projectId,omitempty"`
	ModelID   string          `json:"modelId"`
	Inputs    map[string]any  `json:"inputs"`
	Payload   json.RawMessage `json:"-"`
	// ForceAsync：同步出图的上游也让 handler 不阻塞，冻结积分后立即返回 processing，
	// 调上游+落库丢后台 goroutine。drama 角色生图用，画布不传(保持同步返回 outputUrls)。
	ForceAsync bool
	// Sink 结果去向（见 sink.go）。非空时任务走持久化队列：generation 执行完主动把
	// 结果交给注册的 handler，发起方不必自己轮询。空则维持老语义（调用方自取结果）。
	Sink json.RawMessage `json:"-"`
	// PresetKey：绘图提示词预设的 key（画布 gen-image 专用）。随快照持久化；执行层
	// (prepareExecution)据此把预设 prompt 正文拼进上游请求——正文对前端保密，
	// 绝不能提前拼进 Inputs（GET /tasks 会把 input 快照原样吐回前端）。
	// json:"-" 防直连调用方经通用提交口伪造；产品路径由 canvasgen.Enqueue 显式传入。
	PresetKey string `json:"-"`
	// PresetParams：预设参数的选中档位（维度 key → 档位 key，全是 key 不含正文）。
	// 与 PresetKey 同一套管道：随快照持久化，执行层按 key 现解析片段。
	PresetParams map[string]string `json:"-"`
	// Note：账单「交易详情」的操作标签(如 "项目 · 视频去字幕")，仅项目管理(drama)各调用处传。
	// 非空时账单记 "<Note> · <模型名称>"；为空(画布)回落 "生成 · <modelId>"。json:"-" 防客户端伪造。
	Note string `json:"-"`
	// DramaProjectID/DramaSegmentID：项目管理(drama)归属，用于消耗积分按项目/片段聚合。
	// 0 = 无(画布/agent 不设)。drama 各 submit 构造处设置；冻结/结算/退款三处账本行据此打同一 scope。
	DramaProjectID int64 `json:"-"`
	DramaSegmentID int64 `json:"-"`
}

type SubmitResult struct {
	TaskID      int64           `json:"taskId"`
	Status      string          `json:"status"`
	FrozenCents int64           `json:"frozenCents"`
	Output      json.RawMessage `json:"output,omitempty"`
	OutputURLs  []string        `json:"outputUrls,omitempty"`
	CostCents   *int64          `json:"costCents,omitempty"`
}
