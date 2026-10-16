package generation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/billing"
	"github.com/james/huabu_studio/apps/api/internal/lifecycle"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/upstream"
)

type Billing interface {
	Freeze(ctx context.Context, userID, taskID, amountCredits int64, note string) (BillingResult, error)
	Settle(ctx context.Context, userID, taskID, frozenCredits, actualCredits int64, note string) (BillingResult, error)
	Refund(ctx context.Context, userID, taskID, frozenCredits int64, note string) (BillingResult, error)
	// Tx 版三原语(v5 §5.1)：在调用方的事务里动钱，绝不内部 Commit/Rollback。
	// 「建行与冻结同事务」「终态与收口同事务」都靠它们成立。
	FreezeTx(ctx context.Context, tx *sql.Tx, userID, taskID, amountCredits int64, note string) (BillingResult, error)
	SettleTx(ctx context.Context, tx *sql.Tx, userID, taskID, frozenCredits, actualCredits int64, note string) (BillingResult, error)
	RefundTx(ctx context.Context, tx *sql.Tx, userID, taskID, frozenCredits int64, note string) (BillingResult, error)
}

type BillingResult struct {
	NewBalanceCredits int64
}

type ModelLookup interface {
	GetByModelID(modelID string) (*models.ModelConfig, error)
}

type SettingsLookup interface {
	Get(key string) (string, error)
}

// UpstreamKeyLookup 按任务归属用户解析上游 SK(用户独立 key 优先,回落全局)。
type UpstreamKeyLookup interface {
	Resolve(userID int64) (string, error)
}

// RealPersonResolver 把画布真人图库内已入库图片的 object_key 解析为 asset:// 引用。
// 生产实现由 library 包的 SeedanceService 提供；nil = 功能关闭（替换 hook 跳过）。
type RealPersonResolver interface {
	Resolve(userID int64, objectKey string) (string, error)
}

type AssetStorage interface {
	PutObject(ctx context.Context, input assets.PutObjectInput) (assets.StoredObject, error)
	DeleteObject(ctx context.Context, key string) error
	SignedURL(ctx context.Context, key string, expiry time.Duration) (string, error)
	NewKey(userID int64, filename string) string
	// StaticKey 构造不含时间成分的确定性 key——persistOutputs 的槽位对象用它,
	// 重放覆盖写同一 key,不产生孤儿(v5 §5.5)。
	StaticKey(userID int64, filename string) string
	ExtractKey(urlOrKey string) (string, bool)
	// GetObject 按 key 直读对象字节——inputDuration 懒探测用。走对象存储直读而不是
	// 签名 URL 下载：CLI 路径的 videos[0] 是 media:// 形态，没有可下载的 HTTP URL。
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)
}

type AssetRecorder interface {
	Insert(rec assets.AssetRecord) (int64, error)
	// FindByTaskSlot/InsertSlotTx:槽位幂等(v5 §5.5),见 assets.Repository 注释。
	FindByTaskSlot(taskID int64, outputIndex int) (*assets.AssetRecord, error)
	InsertSlotTx(tx *sql.Tx, rec assets.AssetRecord, outputIndex int) (int64, bool, error)
	ListForTask(taskID int64) ([]assets.AssetRecord, error)
	DeleteForTask(taskID int64) (int64, error)
	// Get/SetDurationMsByObjectKey：inputDuration 时长链复用 assets.duration_ms
	// （上传/生成时已落，NULL 只剩历史资产），语义见 assets.Repository 同名方法。
	GetDurationMsByObjectKey(objectKey string) (int64, bool, error)
	SetDurationMsByObjectKey(objectKey string, durationMs int64) error
}

// UserStorage 是 generation service 所需的 users.Repository 最小子集，
// 用于在任务产物进入 / 离开对象存储时同步 storage_used_bytes。
type UserStorage interface {
	AddStorage(id int64, delta int64) error
	// StorageUsage 供提交闸预检 (used, quota)——见 ensureStorageQuota 的语义注释。
	StorageUsage(id int64) (used int64, quota int64, err error)
	// AddStorageTx:与 asset 行插入同事务累计用量——Insert 成功 AddStorage 失败的
	// 「永久少计存储」窗口(v5 §5.5)随事务消失。
	AddStorageTx(tx *sql.Tx, id int64, delta int64) error
}

// ObjectReclaimer 是删除任务时回收产物对象的窄依赖。参考仓直接调 library.TryReclaim +
// library.IsStillReferenced；桌面端经接口注入（实现由 P1b 的 library 包提供，装配见
// cmd/sidecar），generation 因此不 import library。
type ObjectReclaimer interface {
	// ReclaimTx 在 tx 的可见性下判断 objectKey 是否仍被 assets / library_items 引用；
	// 不再被引用时登记孤儿（7 天后物理删除，spec S9）并返回 true，调用方据此回收存储用量。
	ReclaimTx(tx *sql.Tx, objectKey string, userID, sizeBytes int64) (freed bool, err error)
}

type Service struct {
	repo        Repository
	models      ModelLookup
	settings    SettingsLookup
	upstreamKey UpstreamKeyLookup
	billing     Billing
	upstream    *upstream.Client
	storage     AssetStorage
	assets      AssetRecorder
	users       UserStorage
	reclaimer   ObjectReclaimer
	realPerson  RealPersonResolver
	db          *sql.DB
	httpClient  *http.Client
	// concurrency 每模型并发闸(方案 A):所有生成经 callAndComplete 时按模型取名额。
	// 全平台共用这一个 registry(画布/候选图/材料视频/去字幕/步骤4/资产/agent)。
	concurrency *ConcurrencyRegistry
	// 租约配置：由 main.go 通过 SetLeaseConfig 注入；零值走下面的兜底 getter。
	leaseHeartbeatSec int
	leaseStaleSec     int
	// tmpDir 参考图/multipart/产物的临时落盘目录（SetTempDir 注入）。
	tmpDir string
	// lifecycle 仅测试注入（见 tracker）；生产为 nil，用 lifecycle.Default。
	taskTracker *lifecycle.Tracker
	// finishSuccessHook 仅测试注入：替换 finishSuccessDurable 内部的单次收口。
	finishSuccessHook func(ctx context.Context, taskID, lease int64, out finishOutcome) (bool, error)
	// queueStuckSecCfg 排队滞留兜底秒数，0=用默认 6 小时。
	queueStuckSecCfg int
	pollRetryBase    time.Duration
	pollRetryMax     time.Duration
	// promptPreset 见 SetPromptPresetResolver。
	promptPreset func(key string, params map[string]string) (string, bool)
	// repersist 只供测试注入；nil 时 repersistStore() 回退到 repo（SQLiteRepository 实现 UpdateOutput）。
	repersist RepersistStore
	// publisher 见 SetMediaPublisher（对象存储设计 §3.2）；nil 或未配置 = 参考仓原逻辑。
	publisher MediaPublisher
}

func NewService(
	repo Repository,
	modelLookup ModelLookup,
	settingsLookup SettingsLookup,
	upstreamKey UpstreamKeyLookup,
	billing Billing,
	client *upstream.Client,
	storage AssetStorage,
	assetRepo AssetRecorder,
	userStorage UserStorage,
	reclaimer ObjectReclaimer,
	db *sql.DB,
) *Service {
	return &Service{
		repo:        repo,
		models:      modelLookup,
		settings:    settingsLookup,
		upstreamKey: upstreamKey,
		billing:     billing,
		upstream:    client,
		storage:     storage,
		assets:      assetRepo,
		users:       userStorage,
		reclaimer:   reclaimer,
		db:          db,
		// 600s 是整请求限时(连接+读完全部字节)。曾是 120s:资产生图一次铺几十个角色时
		// 几十条连接同挤一个上游中转域名(Cloudflare R2),单连接带宽掉到几十 KB/s,
		// 4.5MB 的图 120s 拉不完 → persistOutputs 静默保留上游外链 → 上游两周清文件
		// 后永久裂图(线上 87 条)。放宽到 600s 只是止血,兜底是 RunRepersistWorker。
		httpClient:  &http.Client{Timeout: 600 * time.Second},
		concurrency: NewConcurrencyRegistry(),
	}
}

type upstreamPollClient interface {
	Call(ctx context.Context, apiKey, method, path string, payload json.RawMessage) (*upstream.CallResult, error)
}

// SetLeaseConfig 注入租约心跳/超时参数（main.go 在 NewService 后立即调用）。
// 不改 NewService 签名——setter 方式是最小侵入。
func (s *Service) SetLeaseConfig(heartbeatSec, staleSec int) {
	s.leaseHeartbeatSec = heartbeatSec
	s.leaseStaleSec = staleSec
}

// SetQueueConfig 注入队列相关参数（同样用 setter，不动 NewService 签名）。
func (s *Service) SetQueueConfig(stuckSec int) { s.queueStuckSecCfg = stuckSec }

// SetPromptPresetResolver 注入「预设 key + 参数选档 → 完整前置文本」解析器
// （main.go 用 canvasgen 的配置装配，含正文 + 各参数档位片段的拼合）。做成注入
// 而非直接读配置：canvasgen import generation，反向 import 会成环；generation 也
// 不该认识画布预设的存储形状。nil/查不到一律按未选预设静默降级。
func (s *Service) SetPromptPresetResolver(fn func(key string, params map[string]string) (string, bool)) {
	s.promptPreset = fn
}

func (s *Service) WithRealPersonResolver(r RealPersonResolver) { s.realPerson = r }

// heartbeatSec 心跳间隔(秒)，兜底 15。
func (s *Service) heartbeatSec() int {
	if s.leaseHeartbeatSec > 0 {
		return s.leaseHeartbeatSec
	}
	return 15
}

// staleSec 租约超时秒数，兜底 90。
// queueStuckSec 是排队滞留的兜底上限：超过它仍没被领取就判失败退款。
// 默认 6 小时——足够覆盖正常的排队高峰，又不至于让钱冻太久。
func (s *Service) queueStuckSec() int {
	if s.queueStuckSecCfg > 0 {
		return s.queueStuckSecCfg
	}
	return 6 * 3600
}

func (s *Service) staleSec() int {
	if s.leaseStaleSec > 0 {
		return s.leaseStaleSec
	}
	return 90
}

// startHeartbeat 以持有的执行权代次续心跳,返回停止函数。**不 claim**——lease 只能
// 来自领取/接管的 CAS(v5 §5.3 出口矩阵),执行链向下传递,中途绝不重新生成。
// 心跳影响 0 行 = 失租:立刻调 onLost(通常是执行 ctx 的 cancel),尽早中止上游调用;
// 失租者静默退出,不退款不标失败——那次调用的成本平台承担(「设计」§5.4)。
func (s *Service) startHeartbeat(taskID, lease int64, onLost func()) func() {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Duration(s.heartbeatSec()) * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				won, err := s.repo.Heartbeat(taskID, lease)
				if err == nil && !won {
					log.Printf("[gen-lease] task=%d 失租(代次 %d 已被接管),取消执行", taskID, lease)
					onLost()
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }) }
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
}

// ledgerNote 拼账单「交易详情」：操作标签 + 模型「名称」(非 id，更直观)。
// opLabel 为空(画布)时回落旧文案 "生成 · <modelId>"，保持画布零影响。
func ledgerNote(model *models.ModelConfig, opLabel string) string {
	if opLabel == "" {
		return "生成 · " + model.ModelID
	}
	name := model.Name
	if name == "" {
		name = model.ModelID // 模型未设显示名时退回 id，至少不空
	}
	return opLabel + " · " + name
}

type SubmitResult struct {
	TaskID      int64           `json:"taskId"`
	Status      string          `json:"status"`
	FrozenCents int64           `json:"frozenCents"`
	Output      json.RawMessage `json:"output,omitempty"`
	OutputURLs  []string        `json:"outputUrls,omitempty"`
	CostCents   *int64          `json:"costCents,omitempty"`
}

func (s *Service) Submit(ctx context.Context, input SubmitInput) (*SubmitResult, error) {
	if input.ModelID == "" {
		return nil, errors.New("modelId required")
	}
	model, err := s.models.GetByModelID(input.ModelID)
	if err != nil {
		return nil, fmt.Errorf("load model: %w", err)
	}
	if !model.Enabled {
		return nil, errors.New("model disabled")
	}

	// 提交边界按 uiSchema 归一化枚举参数的类型——写入方(前端/LLM 分镜提取/回填)对
	// 枚举值类型把握不一，schema 才是真源（见 uischema_inputs.go 的 WHY）。
	input.Inputs = normalizeInputsToUISchema(model.UISchema, input.Inputs)

	// 归一化后紧跟白名单校验：越界枚举值(如 Regenerate 注入 duration=1 薅按秒计费)
	// 在此拒绝而非放行入队。
	if err := validateInputsAgainstUISchema(model.UISchema, input.Inputs); err != nil {
		return nil, fmt.Errorf("输入校验失败: %w", err)
	}

	// 输入数量约束（gen_control.inputLimits，spec 2026-08-12）：与 uiSchema 白名单
	// 同一梯队，违约在建行/冻结之前拒绝。未配置的模型 len==0 直接放行。
	if err := validateInputLimits(model.InputLimits(), input.Inputs); err != nil {
		return nil, err
	}

	// 计费时长注入（gen_control.inputDuration，spec 2026-08-12）：与 inputLimits
	// 同梯队第三步，在冻结之前注入，EstimateFreeze/SettleAmount/产物 duration_ms
	// 全都以注入值为准。
	if err := s.resolveInputDuration(ctx, model.InputDuration(), input.Inputs); err != nil {
		return nil, err
	}

	// doubao-seedance* 对真人图有上游校验：真人图必须以 asset://<素材库id> 形式引用。
	// 引用图若命中该用户画布真人图库（已入库 active）则自动替换；命中但未就绪直接拒绝，
	// 让用户看到明确原因而不是上游的模糊报错。未命中的图原样透传。
	replaced, err := s.applyRealPersonRefs(input.UserID, model, input.Inputs)
	if err != nil {
		return nil, err
	}
	input.Inputs = replaced

	// 带 sink → 队列模式(v5 §5.4):Submit 是 SubmitTx 的自开事务薄包装。
	// 建行与冻结同事务、行以 queued 出生;网络性准备(签图/预取)完全跳过——
	// 那是执行期材料,队列 worker 的 prepareExecution 会重建。
	if len(input.Sink) > 0 {
		if s.db == nil {
			return nil, errors.New("generation unavailable")
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()
		res, err := s.SubmitTx(ctx, tx, input)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		log.Printf("[gen] task=%d 入队等待执行 model=%s note=%q", res.TaskID, input.ModelID, input.Note)
		return res, nil
	}

	// 存储闸（sinkless 直连路径）：sink 路径由上面的 SubmitTx 内部把关，这里
	// 补上直连提交。同样在冻结（下方 FreezeTx）之前，且在一切网络 IO（签图/
	// 预取/调上游）之前——注定被拒的请求不该花任何上游成本。
	if err := s.ensureStorageQuota(input.UserID); err != nil {
		return nil, err
	}

	apiKey, baseDomain, err := s.upstreamCredentials(input.UserID, model)
	if err != nil {
		return nil, err
	}

	requireURLImageBytes := model.ImageRequestMode == models.ImageRequestModeOpenAIImageEdits
	upstreamInputs, err := s.signInputMediaReferences(ctx, input.Inputs, skipImagesFor(model))
	if err != nil {
		return nil, fmt.Errorf("sign input media references: %w", err)
	}
	normalizedInputs, preparedImages, err := prepareImageInputs(ctx, upstreamInputs, model.ImageInputFormat, requireURLImageBytes, s.fetchInputImage, s.spoolInputImage)
	if err != nil {
		return nil, fmt.Errorf("prepare image inputs: %w", err)
	}
	// 参考图临时文件从这里起归本函数所有，直到交给 callAndComplete（它自己 defer 删）。
	// 之间任何一条错误出口（模板渲染、建行、冻结、提交）都必须删掉，否则文件一直留到
	// 下次启动才被按时效清理。
	handedOff := false
	defer func() {
		if !handedOff {
			removePreparedFiles(preparedImages)
		}
	}()

	payload, err := upstream.InterpolateTemplate(model.RequestTemplate, normalizedInputs)
	if err != nil {
		return nil, fmt.Errorf("interpolate template: %w", err)
	}
	if err := s.ensureNoLocalMediaForRequest(model, payload, preparedImages); err != nil {
		return nil, err
	}

	// 预冻结额只依赖模型配置（按次取固定价，按量取 base + 每单位上限的保守估）。
	// 真正多退少补在 finalize 的 settle 里做。
	mb := model.BillingConfig()
	// 统一计费 req 形态：pricer 的按量/分级路径配的是 req:inputs.duration / req:inputs.n …，
	// 所以传给 EstimateFreeze / SettleAmount 的 req 必须包一层 {"inputs": …}，
	// 否则 req:inputs.* 取不到值、量被当 0 → 漏计费。冻结与结算必须用同一形态。
	freezeAmt := billing.EstimateFreeze(mb, map[string]any{"inputs": normalizedInputs})

	// note：账单交易详情。持久化操作标签(input.Note)到 task.Input，便于重启后孤儿任务恢复时重建。
	note := ledgerNote(model, input.Note)
	inputCapture := map[string]any{
		"modelId": input.ModelID,
		"inputs":  sanitizeTaskSnapshot(input.Inputs),
		"payload": sanitizeTaskPayload(payload),
		"note":    input.Note,
	}
	inputBytes, _ := json.Marshal(inputCapture)

	// sinkless → 直连(v5 §5.4):建行、置 running+owner+lease=1+heartbeat、冻结在
	// 同一个事务里——pending_freeze 中间态与「冻结失败留痕行」随原子性消失:
	// 余额不足整体回滚,任务行根本不存在,记结构化审计日志即可。
	// 全生命周期不经过 queued,worker 结构上领不到(保证 4)。
	if s.db == nil {
		return nil, errors.New("generation unavailable")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	taskID, err := s.repo.CreateTx(tx, Task{
		UserID:      input.UserID,
		ProjectID:   input.ProjectID,
		ModelID:     input.ModelID,
		Status:      "running",
		Input:       inputBytes,
		FrozenCents: freezeAmt,
	})
	if err != nil {
		return nil, fmt.Errorf("persist task: %w", err)
	}
	if err := s.repo.MarkRunningDirectTx(tx, taskID, lifecycle.InstanceID()); err != nil {
		return nil, fmt.Errorf("mark running: %w", err)
	}
	if _, err := s.billing.FreezeTx(ctx, tx, input.UserID, taskID, freezeAmt, note); err != nil {
		if errors.Is(err, billing.ErrInsufficientBalance) {
			log.Printf("[gen-audit] submit rejected: user=%d model=%s 余额不足(需 %d)", input.UserID, input.ModelID, freezeAmt)
			return nil, billing.ErrInsufficientBalance
		}
		return nil, fmt.Errorf("freeze: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	log.Printf("[gen] submit task=%d model=%s note=%q (direct)", taskID, input.ModelID, input.Note)
	const directLease = int64(1) // 建行者即首任持有者(v5 §5.3 矩阵:直连建行 lease=1)

	// ForceAsync：调用方(drama 角色生图)要求 handler 不阻塞。角色用的上游是"一次 Call
	// 同步等出图"的模型，这一调会把整个 HTTP 请求堵几十秒~几分钟。冻结积分已经同步做完
	// (余额不足上面已同步返回)，这里把"调上游+落库"整段丢后台 goroutine，handler 立刻返回
	// processing，前端靠轮询 task 状态拿结果。用独立 ctx：HTTP 一返回，请求的 ctx 就被取消，
	// 后台不能跟着断。
	handedOff = true // 以下两条路径都经 callAndComplete，由它负责删参考图临时文件
	if input.ForceAsync {
		s.tracker().Go(func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("generation: panic in forced-async submit task=%d: %v", taskID, rec)
					_, _ = s.finishFailureTx(context.Background(), taskID, directLease, fmt.Sprintf("internal panic: %v", rec), note)
				}
			}()
			if _, err := s.callAndComplete(bgCtx, taskID, directLease, model, apiKey, baseDomain, payload, input.UserID, input.ProjectID, freezeAmt, normalizedInputs, preparedImages, note); err != nil {
				log.Printf("generation: forced-async submit failed task=%d: %v", taskID, err)
			}
		})
		return &SubmitResult{TaskID: taskID, Status: "processing"}, nil
	}

	return s.callAndComplete(ctx, taskID, directLease, model, apiKey, baseDomain, payload, input.UserID, input.ProjectID, freezeAmt, normalizedInputs, preparedImages, note)
}

// TaskStatuses 批量取轻量状态，供候选图等业务在 List 时投影重试进度（v5 §4.4）。
func (s *Service) TaskStatuses(ids []int64) (map[int64]TaskStatusLite, error) {
	return s.repo.StatusLiteByIDs(ids)
}

func (s *Service) Estimate(_ context.Context, modelID string, inputs map[string]any) (int64, error) {
	if modelID == "" {
		return 0, errors.New("modelId required")
	}
	model, err := s.models.GetByModelID(modelID)
	if err != nil {
		return 0, fmt.Errorf("load model: %w", err)
	}
	if !model.Enabled {
		return 0, errors.New("model disabled")
	}
	return billing.EstimateFreeze(model.BillingConfig(), map[string]any{"inputs": inputs}), nil
}

// callAndComplete 同步调上游，然后按 model 是否声明 pollingTemplate 决定收尾：
//   - 任务式上游(有 pollingTemplate + 上游返回 task id)：标 processing，丢 completeAsync 轮询；
//   - 同步上游：直接 finalize 落库。
//
// Submit 的同步路径直接调它；ForceAsync 路径在后台 goroutine 里用独立 ctx 调它。
func (s *Service) callAndComplete(ctx context.Context, taskID, lease int64, model *models.ModelConfig, apiKey, baseDomain string, payload []byte, userID int64, projectID *int64, freezeAmt int64, inputs map[string]any, preparedImages []preparedImageInput, note string) (*SubmitResult, error) {
	// 并发闸(方案 A):全平台所有生成(画布/候选图/材料视频/去字幕/步骤4/资产/agent)都经此函数,
	// 在这里按模型取一个共享名额(容量=model.GenConcurrency(),缺失取默认 200),持有到整个生成
	// 生命周期结束才释放——inline 出图在本函数返回时释放;task-id 轮询模型(视频)把释放转交给
	// completeAsync,在其收尾时释放。这样"同一模型同时在途的生成数"被真实限住,全平台共用一把闸。
	// 各入口都是 ForceAsync(在后台 goroutine 里跑本函数),故 acquire 阻塞等名额不会卡住 HTTP。
	//
	// lease 由领取/建行的 CAS 取得并传入——本函数绝不重新 claim(v5 §5.3:正常链路只用执行链
	// 传下来的 lease)。心跳必须在等名额**之前**起:直连任务建行即 running,名额被占满(视频模型
	// 整段轮询都持有名额)超过 staleSec 时,没有心跳的行会被接管器当直连孤儿判失败退款。
	// 失租时心跳会 cancel 本 ctx，尽早中止上游调用；心跳在本函数返回时停，轮询类模型由
	// completeAsync 自己续。
	ctx, cancelOnLost := context.WithCancel(ctx)
	defer cancelOnLost()
	stopHeartbeat := s.startHeartbeat(taskID, lease, cancelOnLost)
	defer stopHeartbeat()
	release := s.concurrency.Acquire(model.ModelID, model.GenConcurrency())
	releasedToAsync := false
	defer func() {
		if !releasedToAsync {
			release()
		}
	}()
	// 参考图临时文件的兜底删除点：无论上游成功/失败/失租,本函数返回即清。
	defer removePreparedFiles(preparedImages)

	// 在真正发出请求之前，先把"已调过上游"这个事实落库。
	//
	// 这是接管分流的唯一判据：没有它，被中断的任务全都会被当成"什么都没发生"而重排，
	// 包括那些已经发出请求的——上游会被调第二次，用户被扣两次钱。
	// 写失败必须中止：宁可这次不做，也不能在"外部副作用不可追踪"的状态下调上游。
	if won, err := s.repo.MarkUpstreamCalled(taskID, lease, models.UpstreamFingerprint(baseDomain, apiKey)); err != nil || !won {
		if err == nil {
			// 失租:代次已被接管——静默退出,新持有者会走完(v5 §5.3 失租语义)。
			log.Printf("generation: task=%d MarkUpstreamCalled 失租,静默退出", taskID)
			return nil, fmt.Errorf("任务已被其他实例接管")
		}
		msg := "无法记录上游调用状态，已取消并退款，请重试"
		log.Printf("generation: task=%d MarkUpstreamCalled 失败，中止调用: %v", taskID, err)
		_, _ = s.finishFailureTx(context.Background(), taskID, lease, msg, note)
		return nil, err
	}

	// 每次调用用局部 client：曾经回写 s.upstream，并发任务同写一个字段是数据竞争（M2），
	// 且该字段无人读取。
	client := upstream.NewClient(upstream.ClientOptions{BaseURL: baseDomain, TempDir: s.tempDir()})
	result, err := s.callUpstream(ctx, client, apiKey, model, payload, preparedImages)
	if err != nil {
		// §2 分类:提交调用错误一律保守终败退款(权威拒绝无随机性重试无意义;
		// 不确定错误无 ID 且已标 called 只能退款)。失租时 CAS 落空,finish 静默不动钱。
		_, _ = s.finishFailureTx(context.Background(), taskID, lease, err.Error(), note)
		return nil, err
	}
	// 产物临时文件的兜底删除点：finalize 成功/失败/失租都会回到这里。
	defer result.RemoveSpillFiles()

	// 当 model 的 pollingTemplate 配置了 taskIdField 时，应用这个显式提示。
	// 它优先于 ExtractFields 内置的 task_id / id / result 兜底链——适用于
	// 上游使用了独特命名字段的代理。
	if len(model.PollingTemplate) > 0 {
		var pcfg PollingConfig
		if err := json.Unmarshal(model.PollingTemplate, &pcfg); err == nil && pcfg.TaskIdField != "" {
			var body map[string]any
			if err := json.Unmarshal(result.Body, &body); err == nil {
				if v, ok := getNestedString(body, pcfg.TaskIdField); ok && v != "" {
					result.TaskID = v
				}
			}
		}
	}

	// 异步路径：model 声明了 pollingTemplate 且上游返回了 task id。
	// 把任务标记为 processing，立即返回，剩下的在后台 goroutine 里完成，
	// 这样 HTTP 请求不会被阻塞好几分钟。
	if len(model.PollingTemplate) > 0 && result.TaskID != "" {
		if won, err := s.repo.MarkUpstream(taskID, lease, result.TaskID); err != nil {
			log.Printf("generation: MarkUpstream failed for task %d: %v", taskID, err)
		} else if !won {
			log.Printf("generation: task=%d MarkUpstream 失租,静默退出", taskID)
			return nil, fmt.Errorf("任务已被其他实例接管")
		}
		// 把并发名额的释放转交给 completeAsync：名额要持有到上游真出片(轮询结束)才归还,
		// 不能在本函数返回 processing 时就释放(那样"限并发"形同虚设)。
		releasedToAsync = true
		s.tracker().Go(func() {
			defer release()
			s.completeAsync(taskID, lease, model, apiKey, baseDomain, result.TaskID, userID, projectID, freezeAmt, inputs, note, time.Now())
		})
		return &SubmitResult{
			TaskID: taskID,
			Status: "processing",
		}, nil
	}

	// 同步路径：model 直接内联返回结果（生图等）。
	return s.finalize(ctx, taskID, lease, model, userID, projectID, freezeAmt, inputs, result, note)
}

func (s *Service) callUpstream(ctx context.Context, client *upstream.Client, apiKey string, model *models.ModelConfig, payload []byte, preparedImages []preparedImageInput) (*upstream.CallResult, error) {
	if model.ImageRequestMode == models.ImageRequestModeOpenAIImageEdits && len(preparedImages) > 0 {
		path := model.BasePath
		if path == "/v1/images/generations" {
			path = "/v1/images/edits"
		}
		files := multipartFilesFromPrepared(preparedImages)
		if len(files) == 0 {
			return nil, fmt.Errorf("image edits request has no image files")
		}
		return client.CallMultipart(ctx, apiKey, "POST", path, scalarMultipartFields(payload), files)
	}
	return client.Call(ctx, apiKey, "POST", model.BasePath, payload)
}

func scalarMultipartFields(payload []byte) map[string]string {
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil
	}
	fields := make(map[string]string)
	for key, value := range body {
		if key == "image" || key == "images" || key == "mask" {
			continue
		}
		switch v := value.(type) {
		case string:
			fields[key] = v
		case bool:
			fields[key] = strconv.FormatBool(v)
		case float64:
			fields[key] = strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return fields
}

// finalize 结算计费、把产物持久化到 MinIO 并经 finishSuccessTx 原子收口。
// 同步 submit 路径和异步 goroutine 共用它。幂等闸门就是 finishTx 里带 lease 的
// 终态 CAS——settling 中间态与 ClaimSettlement 不再需要(v5 §5.2)。
func (s *Service) finalize(ctx context.Context, taskID, lease int64, model *models.ModelConfig, userID int64, projectID *int64, frozenCredits int64, reqInputs map[string]any, result *upstream.CallResult, note string) (*SubmitResult, error) {
	// 出结果后多退少补：按模型计费配置 + 请求/响应 map 算实际积分，
	// 与冻结额结算。
	mb := model.BillingConfig()
	var respMap map[string]any
	_ = json.Unmarshal(result.Body, &respMap)
	// 与 Submit 的冻结同形态：pricer 配的是 req:inputs.*，故包一层 {"inputs": …}。
	// 所有 finalize 调用方传进来的 reqInputs 都是裸 inputs，这里统一包，保证
	// 冻结额与结算额走相同的取值路径，不会冻一套量、结另一套量。
	actualCredits, settleErr := billing.SettleAmount(mb, map[string]any{"inputs": reqInputs}, respMap)
	auditFlag := "credits_settled"
	if settleErr != nil {
		// 算不出实际量(按量配错/上游没回 usage):按实际冻结额结算,不退不补,
		// 标记审计——宁可按已冻的全额收,也不静默漏计费。
		actualCredits = frozenCredits
		auditFlag = "settle_fallback_freeze"
		log.Printf("generation: settle amount failed task=%d: %v (fallback to freeze)", taskID, settleErr)
	}
	// 在 persistOutputs 覆盖 result.OutputURLs 前，快照上游原始地址（仅 http(s)），
	// 供 sd2.0 限时去字幕直连使用。sink handler 仅对 seedance 模型回填到候选。
	upstreamURLs := filterHTTPURLs(result.OutputURLs)

	durMs := durationMsFromInputs(reqInputs)
	persistedURLs, assetIDs, mimes, perr := s.persistOutputs(ctx, userID, taskID, projectID, result.OutputURLs, durMs)
	if perr != nil {
		// 只有 file:// / data: 槽位会走到这里（persistOutputs 注释）。上游已出图、钱已冻结,
		// 但产物没法交付——保守退款,与「上游调用错误」同一收口。
		_, _ = s.finishFailureTx(context.Background(), taskID, lease, "产物保存失败，已退款，请重试", note)
		return nil, perr
	}
	result.OutputURLs = persistedURLs

	// 故意不存上游的原始 body —— 有些 model（Gemini 图像）会内联返回几 MB 的
	// base64，会把这一行撑大到 tasks 表上 ORDER BY id 耗尽 sort_buffer_size、
	// 静默返回零行的程度。媒体已经通过 outputUrls 持久化了。
	output := map[string]any{
		"outputUrls": result.OutputURLs,
	}
	// assetIds 与 outputUrls 同序一一对应（落库失败处为 0）。画布节点靠它做
	// 「保存到素材库」「Seedance 合规校验」——这些操作需要 asset 身份，光有 URL 不够。
	if len(assetIDs) > 0 {
		output["assetIds"] = assetIDs
	}
	// outputKind：主产物(index 0)mime 的大类，供前端历史面板判视频/图片用。
	// 存在的必要性见 outputKindFromMime 注释——隐藏模型 + 无扩展名对象 key
	// 会让前端纯靠猜测判错类型，这里补一个后端算好的真实信号。
	// 同一个值也写进 output_kind 列(0103)——列供「生成历史选择」按类型过滤,
	// JSON 供前端展示,两处同源不会分叉。
	outputKind := ""
	if len(mimes) > 0 {
		if kind := outputKindFromMime(mimes[0]); kind != "" {
			output["outputKind"] = kind
			outputKind = kind
		}
	}
	if len(upstreamURLs) > 0 {
		output["upstreamUrls"] = upstreamURLs
	}
	if result.TaskID != "" {
		output["upstreamTaskId"] = result.TaskID
	}
	// 文本产物（chat 模型的正文）：仅在没有任何媒体产物时才算数——生图类 chat
	// 模型的回复文本只是图片附带说明（CallResult.Text 注释）。文本直接进 output
	// JSON 列，不走 persistOutputs（没有对象存储环节）。
	taskText := ""
	if len(result.OutputURLs) == 0 && result.Text != "" {
		taskText = result.Text
		output["text"] = taskText
	}
	outputBytes, _ := json.Marshal(output)

	// 原子收口:终态 CAS + SettleTx + 插 outbox 同一事务(v5 §5.2)。
	// persistOutputs 在其之前执行——崩在两者之间时,接管重放靠槽位幂等不产生重复资产。
	// I3：收口不随执行 ctx 取消，瞬时错误有界重试（finishSuccessDurable）。
	won, err := s.finishSuccessDurable(ctx, taskID, lease, finishOutcome{
		output:        outputBytes,
		outputKind:    outputKind,
		actualCredits: actualCredits,
		auditFlag:     auditFlag,
		note:          note,
		sinkRes: SinkResult{
			ModelID:      model.ModelID,
			OutputURLs:   result.OutputURLs,
			UpstreamURLs: upstreamURLs,
			AssetIDs:     assetIDs,
			Text:         taskText,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("finish success: %w", err)
	}
	if !won {
		// 失租:新持有者会走完(含收口)。同步调用方拿到明确错误。
		return nil, fmt.Errorf("任务已被其他实例接管")
	}

	cost := actualCredits
	return &SubmitResult{
		TaskID:     taskID,
		Status:     "succeeded",
		Output:     outputBytes,
		OutputURLs: result.OutputURLs,
		CostCents:  &cost,
	}, nil
}

// ResumeOrphanedTasks 扫描那些被留在 'processing' 状态的任务（例如 server
// 重启杀掉了它们的 goroutine），并为每个重新拉起完成 goroutine。
// 可以安全地在启动时从一个 goroutine 调用。
func (s *Service) ResumeOrphanedTasks() {
	if s.draining() {
		return
	}
	tasks, err := s.repo.ListProcessing(0, s.staleSec())
	if err != nil {
		log.Printf("generation: ResumeOrphanedTasks list failed: %v", err)
		return
	}
	if len(tasks) == 0 {
		return
	}
	resumed := 0
	for _, task := range tasks {
		model, err := s.models.GetByModelID(task.ModelID)
		if err != nil {
			log.Printf("generation: resume task %d: model %s missing: %v", task.ID, task.ModelID, err)
			continue
		}
		// 逐任务解析上游:不同模型可能单独配了上游(每模型独立上游),没配的回落全局。
		apiKey, baseDomain, err := s.upstreamCredentials(task.UserID, model)
		if err != nil {
			log.Printf("generation: resume task %d: upstream unavailable (model %s): %v", task.ID, task.ModelID, err)
			continue
		}
		// 从持久化的 task.Input（形如 {"modelId":...,"inputs":{...},"note":...}）
		// 解出原始 inputs(settle 按量计费需要) + 操作标签(重建账单交易详情)。
		var reqInputs map[string]any
		var inputCapture struct {
			Inputs map[string]any `json:"inputs"`
			Note   string         `json:"note"`
		}
		if err := json.Unmarshal(task.Input, &inputCapture); err == nil {
			reqInputs = inputCapture.Inputs
		}
		// 接管必须先 CAS 取得新代次(v5 §5.3):抢不到 = 心跳其实还活着/别的 leader 先接了,
		// 都不该动。ClaimLease 那种无条件覆盖已删除——「接管」现在在 DB 层面是真的。
		lease, won, err := s.repo.ClaimStaleRunning(task.ID, lifecycle.InstanceID(), s.staleSec())
		if err != nil || !won {
			continue
		}
		// I-1：上游配置在任务进行中被改过（换地址/换 Key/切回全局），当前凭据查不到这个上游
		// 任务——上游对 4xx 一律按"不确定"退避重试，会白等整个轮询窗口（最长 24h）。立即失败退款，
		// 文案带上游任务 ID 方便用户去原账号找。旧任务没有指纹，放行。
		if upstreamFingerprintChanged(task.UpstreamFingerprint, baseDomain, apiKey) {
			log.Printf("generation: resume task %d: 上游指纹不一致，判失败 (model %s)", task.ID, task.ModelID)
			_, _ = s.finishFailureTx(context.Background(), task.ID, lease, upstreamChangedMessage(task.UpstreamTaskID), ledgerNote(model, inputCapture.Note))
			continue
		}
		if len(model.PollingTemplate) == 0 {
			log.Printf("generation: resume task %d: model %s has no pollingTemplate, finishing failed", task.ID, task.ModelID)
			// 旧代码只标失败不退款(:679 的洞)——finishFailureTx 原子退款,自动修好。
			_, _ = s.finishFailureTx(context.Background(), task.ID, lease, "orphaned: model no longer configured for polling", ledgerNote(model, inputCapture.Note))
			continue
		}
		s.tracker().Go(func() {
			s.completeAsync(task.ID, lease, model, apiKey, baseDomain, task.UpstreamTaskID, task.UserID, task.ProjectID, task.FrozenCents, reqInputs, ledgerNote(model, inputCapture.Note), task.CreatedAt)
		})
		resumed++
	}
	log.Printf("generation: ResumeOrphanedTasks resumed %d/%d processing tasks", resumed, len(tasks))
}

// upstreamFingerprintChanged 报告任务提交时记下的上游指纹与当前凭据是否不一致；空指纹（旧任务）视为一致。
func upstreamFingerprintChanged(recorded, baseDomain, apiKey string) bool {
	return recorded != "" && recorded != models.UpstreamFingerprint(baseDomain, apiKey)
}

func upstreamChangedMessage(upstreamTaskID string) string {
	return "该任务提交时使用的上游配置已变更，无法继续查询结果（上游任务 ID：" + upstreamTaskID + "）"
}

// completeAsync 在自己的 goroutine 里跑，跟 HTTP 请求解耦：
// 轮询上游直到任务结束，出错就退款/标记失败，否则结算并持久化。
func (s *Service) completeAsync(taskID, lease int64, model *models.ModelConfig, apiKey, baseDomain, upstreamTaskID string, userID int64, projectID *int64, frozenCredits int64, reqInputs map[string]any, note string, startedAt time.Time) {
	defer func() {
		if rec := recover(); rec != nil {
			errMsg := fmt.Sprintf("internal panic during async completion: %v", rec)
			log.Printf("generation: panic in completeAsync task=%d: %v", taskID, rec)
			// 用独立 ctx:触发 defer 时函数的 ctx 多半已超时/取消,收口不能跟着失败。
			_, _ = s.finishFailureTx(context.Background(), taskID, lease, errMsg, note)
		}
	}()

	// 轮询窗口按任务已消耗的寿命折算：新提交给全量 maxWait；孤儿接管(发版强杀后
	// leader 续跑)只给剩余额度，避免每次接管都重置整窗、任务总超时翻倍。
	// 兜底 ctx = 窗口+缓冲，保证窗口的友好文案先于 ctx 超时触发。
	window := remainingPollWindow(startedAt, maxWaitSeconds(model.PollingTemplate))
	ctx, cancel := context.WithTimeout(context.Background(), pollCtxTimeout(window))
	defer cancel()
	stopHeartbeat := s.startHeartbeat(taskID, lease, cancel)
	defer stopHeartbeat()

	client := upstream.NewClient(upstream.ClientOptions{BaseURL: baseDomain})
	polled, err := s.pollUpstream(ctx, client, apiKey, model.PollingTemplate, upstreamTaskID, window)
	if err != nil {
		log.Printf("generation: pollUpstream failed task=%d: %v", taskID, err)
		// 权威终态失败先试业务重试（v5 §4.2）：同一任务转 retry_pending 到点重排，
		// 钱不动、不投递——这不是终态。sinkless/未配重试/次数耗尽时抢不到，落终败。
		var term *upstreamTerminalError
		if errors.As(err, &term) {
			if sink, sErr := s.repo.SinkForTask(taskID); sErr == nil {
				if s.tryScheduleRetry(taskID, lease, model, sink, term.msg) {
					return
				}
			}
		}
		// 用独立 ctx 收口：轮询失败最常见的原因就是本函数的 ctx 超时/取消,
		// 拿死 ctx 去跑退款事务必然失败,用户冻结额会永久卡死(生产 8101/8102 实证)。
		_, _ = s.finishFailureTx(context.Background(), taskID, lease, pollFailureMessage(err), note)
		return
	}
	if polled.TaskID == "" {
		polled.TaskID = upstreamTaskID
	}

	if _, err := s.finalize(ctx, taskID, lease, model, userID, projectID, frozenCredits, reqInputs, polled, note); err != nil {
		log.Printf("generation: finalize failed for task %d: %v", taskID, err)
	}
}

type PollingConfig struct {
	Method         string `json:"method"`
	Path           string `json:"path"`
	Interval       int    `json:"interval"`
	MaxWait        int    `json:"maxWait"`
	StatusField    string `json:"statusField"`
	CompletedValue string `json:"completedValue"`
	FailedValue    string `json:"failedValue"`
	// FailedValues 是 FailedValue 的多值变体。非空时，该列表里的任意状态
	// 也会把任务标记为失败。适用于有 FAILURE / ERROR / CANCELLED 等多个权威失败终态的代理。
	// FailedValue（单值）为了兼容已有视频 model 配置而保留。
	FailedValues []string `json:"failedValues"`
	URLField     string   `json:"urlField"`
	ContentPath  string   `json:"contentPath"`
	// ErrorField 是读取失败原因时用的显式点分路径，指向 response body。
	// 为空时会自动尝试一组常见的上游路径。
	ErrorField string `json:"errorField"`
	// TaskIdField 是读取 upstream task id 时用的显式点分路径，指向 SUBMIT
	// 的 response body，优先于 upstream.ExtractFields 里默认的
	// task_id / id / result 兜底链。用于 task id 字段命名独特（少见）的代理。
	TaskIdField string `json:"taskIdField"`
}

// maxPollWindowSec 是后台可配 maxWait 的生效上限：24 小时。
//
// 为什么是 24h 而不是不设限：轮询期间用户积分一直冻结、上游任务记录也有寿命，
// 窗口大到一定程度就只是把「告诉用户失败」无限推迟。24h 已覆盖超低价上游
// （cm-seedance mini 档）实测的超长排队；配置超过它的部分按 24h 生效。
//
// 曾经这个上限藏在 pollCtxTimeout 的 4h clamp 里：后台明明允许填 86400，
// 轮询却在 4h 被 ctx 静默掐断、报「生成超时」退款——配置页与实际行为不一致，
// 且掐断走的是 ctx 文案而不是窗口的友好文案。上限现在收口在窗口本身，
// ctx 回归它的本职：只兜单次 HTTP 挂死，永远比窗口晚触发。
const maxPollWindowSec = 24 * 60 * 60

// maxWaitSeconds 解析模型轮询模板的 maxWait，未配/模板非法时与 pollUpstream 的默认值(300s)一致。
func maxWaitSeconds(templateRaw json.RawMessage) int {
	var cfg PollingConfig
	_ = json.Unmarshal(templateRaw, &cfg)
	if cfg.MaxWait <= 0 {
		return 300
	}
	if cfg.MaxWait > maxPollWindowSec {
		return maxPollWindowSec
	}
	return cfg.MaxWait
}

// remainingPollWindow 按任务寿命折算剩余轮询窗口：maxWait - 已消耗时间，保底 2 分钟。
// 保底的意义：孤儿接管时上游可能早已完成，pollUpstream 首轮立即查询，2 分钟足够把
// 结果捞回落库，而不是直接判超时退款把已生成的结果丢掉。startedAt 由 parseTime 按
// UTC 解析、time.Since 比较的是绝对时刻，不受 SQLite 以 UTC 文本存时间的影响。
func remainingPollWindow(startedAt time.Time, maxWaitSec int) time.Duration {
	window := time.Duration(maxWaitSec)*time.Second - time.Since(startedAt)
	if window < 2*time.Minute {
		return 2 * time.Minute
	}
	return window
}

// pollCtxTimeout 由轮询窗口推导兜底 ctx 时长：窗口 + 2 分钟缓冲，保证窗口的友好文案
// 永远先于 ctx 超时触发；ctx 只兜单次 HTTP 请求挂死这类极端情况。
//
// clamp 必须比窗口上限（maxPollWindowSec）大：窗口经 maxWaitSeconds 钳到 24h 以内，
// 这里 24h+2min 只是 goroutine 常驻的最后防线，正常路径永远碰不到。曾经它是 4h、
// 比窗口上限还小——ctx 越权成了真正的限流者，把后台配的 24h 静默压成 4h。
func pollCtxTimeout(window time.Duration) time.Duration {
	timeout := window + 2*time.Minute
	if max := time.Duration(maxPollWindowSec)*time.Second + 2*time.Minute; timeout > max {
		return max
	}
	return timeout
}

// pollFailureMessage 把写进 task.error 给用户看的失败原因做最小翻译：
// ctx 超时的原始文本 "context deadline exceeded" 用户看不懂，换成可行动的中文；
// 其余错误原样透传（上游错误体本身通常带信息量）。
func pollFailureMessage(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "生成超时，请重试"
	}
	return err.Error()
}

// window > 0 时覆盖模板 maxWait 作为本次轮询的时间窗——孤儿接管传折算后的剩余窗口；
// window <= 0 走模板配置（既有测试与无窗口调用方的兼容路径）。
func (s *Service) pollUpstream(ctx context.Context, client upstreamPollClient, apiKey string, templateRaw json.RawMessage, upstreamTaskID string, window time.Duration) (*upstream.CallResult, error) {
	var cfg PollingConfig
	if err := json.Unmarshal(templateRaw, &cfg); err != nil {
		return nil, fmt.Errorf("parse polling template: %w", err)
	}
	if cfg.Method == "" {
		cfg.Method = "GET"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 5
	}
	if cfg.MaxWait <= 0 {
		cfg.MaxWait = 300
	}
	if cfg.MaxWait > maxPollWindowSec {
		cfg.MaxWait = maxPollWindowSec
	}
	if cfg.StatusField == "" {
		cfg.StatusField = "status"
	}
	if cfg.CompletedValue == "" {
		cfg.CompletedValue = "completed"
	}
	if cfg.FailedValue == "" {
		cfg.FailedValue = "failed"
	}

	pollPath := strings.ReplaceAll(cfg.Path, "{{task_id}}", upstreamTaskID)
	effectiveWindow := time.Duration(cfg.MaxWait) * time.Second
	if window > 0 {
		effectiveWindow = window
	}
	deadline := time.Now().Add(effectiveWindow)
	normalInterval := time.Duration(cfg.Interval) * time.Second
	nextDelay := time.Duration(0)
	temporaryFailures := 0

	for {
		if nextDelay > 0 {
			if err := waitWithContext(ctx, nextDelay); err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("polling timeout after %ds", int(effectiveWindow.Seconds()))
		}

		result, err := client.Call(ctx, apiKey, cfg.Method, pollPath, nil)
		if err != nil {
			// §2 分类表:轮询 GET 的一切错误形态(4xx 任务不存在/参数错、EOF、5xx、
			// 解析失败)都是「不确定」——上游"查不到"不等于"没在跑",保留 ID 按退避
			// 续轮到 max poll 窗口,绝不提前终败(codex 实施审查 B2)。
			// 唯一例外:本地 ctx 已死,续轮无意义。
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			temporaryFailures++
			nextDelay = s.nextPollingRetryDelay(temporaryFailures, normalInterval)
			continue
		}

		var body map[string]any
		if err := json.Unmarshal(result.Body, &body); err != nil {
			temporaryFailures++
			nextDelay = s.nextPollingRetryDelay(temporaryFailures, normalInterval)
			continue
		}

		temporaryFailures = 0
		status, _ := getNestedString(body, cfg.StatusField)
		if status == cfg.CompletedValue {
			// 提取视频 URL
			if cfg.URLField != "" {
				if urlVal, ok := getNestedString(body, cfg.URLField); ok && urlVal != "" {
					result.OutputURLs = append(result.OutputURLs, urlVal)
				}
			}
			// 没有 URL 字段时，尝试 contentPath（Sora：下载视频流）
			if len(result.OutputURLs) == 0 && cfg.ContentPath != "" {
				contentPath := strings.ReplaceAll(cfg.ContentPath, "{{task_id}}", upstreamTaskID)
				contentResult, err := client.Call(ctx, apiKey, "GET", contentPath, nil)
				if err != nil {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					temporaryFailures = 1
					nextDelay = s.nextPollingRetryDelay(temporaryFailures, normalInterval)
					continue
				}
				if len(contentResult.Body) > 0 {
					// Body 是原始视频字节，转成 data URL
					dataURL := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(contentResult.Body)
					result.OutputURLs = append(result.OutputURLs, dataURL)
				}
			}
			// 同时也尝试标准字段提取
			upstream.ExtractFields(result.Body, result)
			return result, nil
		}
		if statusIsFailed(status, cfg) {
			errMsg := extractErrorMessage(body, cfg.ErrorField)
			if errMsg == "" {
				// 最后兜底：带上原始 body，这样在没有已知路径匹配时能调试。
				errMsg = "upstream generation failed: " + string(result.Body)
			}
			// 类型化标记「权威终态失败」（v5 §2 分类表第一行）——只有它能进业务重试。
			// transport 错/解析失败/本地超时走其他 return，天然不带此类型。
			return nil, &upstreamTerminalError{msg: errMsg}
		}
		// WHY: 这里只给 GET polling 退避；POST submit 不自动重放，
		// 因为上游幂等性不可信，盲目重放可能重复落单/重复扣费。
		nextDelay = normalInterval
	}
}

func (s *Service) nextPollingRetryDelay(attempt int, normalInterval time.Duration) time.Duration {
	delay := s.pollRetryBaseDuration()
	if attempt > 1 {
		for i := 0; i < attempt-1; i++ {
			if delay >= s.pollRetryMaxDuration() {
				break
			}
			delay *= 2
		}
	}
	if maxDelay := s.pollRetryMaxDuration(); maxDelay > 0 && delay > maxDelay {
		delay = maxDelay
	}
	if delay < normalInterval {
		return normalInterval
	}
	return delay
}

func (s *Service) pollRetryBaseDuration() time.Duration {
	if s.pollRetryBase > 0 {
		return s.pollRetryBase
	}
	return time.Second
}

func (s *Service) pollRetryMaxDuration() time.Duration {
	if s.pollRetryMax > 0 {
		return s.pollRetryMax
	}
	return 30 * time.Second
}

func waitWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func getNestedString(m map[string]any, field string) (string, bool) {
	parts := strings.Split(field, ".")
	current := any(m)
	for _, p := range parts {
		obj, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current = obj[p]
	}
	s, ok := current.(string)
	return s, ok
}

// getNested 在一个 JSON 解码后的 map 里按点分 key 走，把该路径上的值作为
// 通用 any 返回。当任一段缺失或不是 map 时返回 nil。
func getNested(m map[string]any, field string) any {
	parts := strings.Split(field, ".")
	current := any(m)
	for _, p := range parts {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = obj[p]
	}
	return current
}

// statusIsFailed 在轮询到的 status 匹配配置的单个 FailedValue 或 FailedValues
// 里任意值时返回 true。单值是为了兼容已有视频 model 的 pollingTemplate JSON 而保留。
func statusIsFailed(status string, cfg PollingConfig) bool {
	if cfg.FailedValue != "" && status == cfg.FailedValue {
		return true
	}
	for _, v := range cfg.FailedValues {
		if status == v {
			return true
		}
	}
	return false
}

// extractErrorMessage 遍历一组常见的上游 error 字段路径，返回找到的第一个
// 非空字符串。如果 polling 配置里提供了 errorField，会先尝试它。返回的字符串
// 是我们能提取到的最有用的、人类可读的失败原因。
func extractErrorMessage(body map[string]any, configPath string) string {
	candidates := []string{}
	if configPath != "" {
		candidates = append(candidates, configPath)
	}
	candidates = append(candidates,
		"data.fail_reason",        // newapi 的失败摘要
		"data.data.error.message", // 豆包/seedance 原生 error.message
		"data.data.error.code",    // 豆包/seedance 原生 error.code
		"data.error.message",      // 更浅一层的豆包变体
		"error.message",           // 通用 OpenAI 风格的顶层
		"message",                 // 通用顶层
		"data.message",            // newapi 风格的顶层 message
	)
	for _, p := range candidates {
		if s, ok := getNestedString(body, p); ok && s != "" {
			return s
		}
	}
	return ""
}

func (s *Service) Get(ctx context.Context, userID, taskID int64) (*Task, error) {
	return s.repo.Get(userID, taskID)
}

// LatestByCanvasNode 供 canvasgen 的节点自愈对账直通 repo（归属校验在 SQL 的 user_id 条件里）。
func (s *Service) LatestByCanvasNode(ctx context.Context, userID, projectID int64, nodeID string) (*Task, error) {
	return s.repo.LatestByCanvasNode(userID, projectID, nodeID)
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]Task, int64, error) {
	return s.repo.List(filter)
}

// Delete 删除一个**终态**任务及其产物引用——行删除、资产行删除、orphan 标记、
// 配额回收在**同一个事务**里(codex 复核修正):任一步失败整体回滚,行还在、可重试,
// 不存在「行没了清理没做完」的不可恢复残留。非终态在事务内被 DeleteTx 拦下(409)。
func (s *Service) Delete(ctx context.Context, userID, taskID int64) error {
	if s.db == nil {
		return errors.New("delete unavailable")
	}
	// 快照 assets:失败必须中止(codex 复核:没有 object key 快照就删行,
	// 对象会失去一切数据库引用且永远进不了 orphan GC)。
	var ownedAssets []assets.AssetRecord
	if s.assets != nil {
		recs, err := s.assets.ListForTask(taskID)
		if err != nil {
			return fmt.Errorf("snapshot task assets: %w", err)
		}
		for _, a := range recs {
			if a.UserID != userID {
				continue
			}
			ownedAssets = append(ownedAssets, a)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// 终态门在事务内:非终态 → ErrTaskNotTerminal 回滚,零副作用。
	if err := s.repo.DeleteTx(tx, userID, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM assets WHERE source_task_id=? AND user_id=?`, taskID, userID); err != nil {
		return err
	}
	var freed int64
	if s.reclaimer != nil {
		for _, a := range ownedAssets {
			// 在本事务的可见性下判断引用：asset 行刚在上面删掉，无其他引用的对象登记孤儿、延后物理删除。
			ok, err := s.reclaimer.ReclaimTx(tx, a.ObjectKey, userID, a.SizeBytes)
			if err != nil {
				return err
			}
			if ok {
				freed += a.SizeBytes
			}
		}
	}
	// 配额回收同事务：任一步失败整体回滚，行还在、可重试。
	if s.users != nil && freed > 0 {
		if err := s.users.AddStorageTx(tx, userID, -freed); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// outputKindFromMime 把产物 mime 的大类映射成前端历史面板判类型用的 outputKind。
// 存在的意义：隐藏模型（如 sd2.0 去字幕）不进 /models 列表，前端拿不到
// model.type 会退化成看 URL 扩展名——但产物对象 key 是 gen/task-N-i，没有
// 扩展名，于是被误判成图片。mime 是下载产物时实测到的，比猜后缀可靠。
// 未知/空 mime 返回 ""，前端据此回退旧逻辑，不影响没有 outputKind 的旧任务。
func outputKindFromMime(mime string) string {
	switch {
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case strings.HasPrefix(mime, "image/"):
		return "image"
	default:
		return ""
	}
}

// filterHTTPURLs 只保留 http(s) 开头的 URL——上游原始视频地址用于 sd2.0 限时去字幕；
// 跳过 data:（Sora 把视频内联成 base64 data URL，存进 tasks 行会撑爆 sort_buffer）和裸对象键。
func filterHTTPURLs(urls []string) []string {
	var out []string
	for _, u := range urls {
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			out = append(out, u)
		}
	}
	return out
}

// persistOutputs 下载每个 output URL（或解码 data URL）并重新上传到 MinIO，
// 这样不论上游存活时长或内联 base64 blob 如何，任务都返回稳定的对象存储 URL。
//
// 每个持久化文件的副作用：
//   - 插入一条带 project_id + source_task_id 的 `assets` 行，这样后续按
//     project 或按 task 删除时能同时释放 DB 行和 TOS 对象。
//   - 把用户的 storage_used_bytes 加上该文件大小，让生成的媒体跟上传一样
//     计入配额。
//
// persistOutputs 落地生成产物，返回持久化后的 URL、与之一一对应的 asset id、
// 以及每个产物的 mime type（finalize 用 mimes[0] 推 output.outputKind）。
// 回传 asset id 是必要的：画布节点拿到它才能做「保存到素材库」「Seedance 合规校验」
// 这类需要 asset 身份的操作——以前只回 URL，前端只能干看着。
// 抓取或落库失败的位置 asset id 为 0、mime 为空串，调用方据此判断不可用。
// durationMsFromInputs 从生成入参取时长(毫秒)。生成视频按此 duration 生成,故它就是产物真实时长。
// 支持 "5s"/5/5.0/json.Number 形态;无 duration 字段(如固定时长模型)→ 0(留 NULL,Part B 计费关口懒回填兜)。
func durationMsFromInputs(inputs map[string]any) int64 {
	v, ok := inputs["duration"]
	if !ok {
		return 0
	}
	var sec float64
	switch t := v.(type) {
	case float64:
		sec = t
	case int:
		sec = float64(t)
	case int64:
		sec = float64(t)
	case json.Number:
		f, _ := t.Float64()
		sec = f
	case string:
		fmt.Sscanf(strings.TrimSpace(t), "%f", &sec)
	}
	if sec <= 0 {
		return 0
	}
	return int64(sec*1000 + 0.5)
}

// errLocalPersist：file:// 产物（gpt-image 落盘的 b64）上传失败。与 http(s) 产物
// 「留上游 URL、任务照常成功」不同,本地文件没法给用户看,只能判失败退款。
var errLocalPersist = errors.New("local output persist failed")

func (s *Service) persistOutputs(ctx context.Context, userID, taskID int64, projectID *int64, urls []string, durationMs int64) ([]string, []int64, []string, error) {
	if s.storage == nil || len(urls) == 0 {
		return urls, nil, nil, nil
	}
	pid := int64(0)
	if projectID != nil {
		pid = *projectID
	}
	out := make([]string, 0, len(urls))
	assetIDs := make([]int64, 0, len(urls))
	mimes := make([]string, 0, len(urls))
	for i, rawURL := range urls {
		if rawURL == "" {
			continue
		}
		// 槽位幂等(v5 §5.5)第一步:先查 (task_id, output_index)——重放命中直接复用,
		// 不下载不上传。`i` 是原始 urls 下标,槽位在重放间稳定(依赖上游输出顺序稳定,
		// 该假设仅在「终态提交后重放」的窄场景生效,乱序最坏是槽位内容互换,无资损)。
		if s.assets != nil {
			if existing, err := s.assets.FindByTaskSlot(taskID, i); err == nil && existing != nil {
				out = append(out, existing.URL)
				assetIDs = append(assetIDs, existing.ID)
				mimes = append(mimes, existing.MimeType)
				continue
			}
		}
		stored, assetID, mime, err := s.persistSlotWithRetry(ctx, userID, taskID, pid, i, rawURL, durationMs)
		if err != nil {
			// file:// 与 data: 都是"内容只在本进程手里"的产物：没有上游地址可留、也无从
			// 手动重试，落盘失败只能判失败退款。data: 绝不能原样写进 output/画布（M4）：
			// 几 MB base64 会撑大快照，且节点没有重试入口。
			if strings.HasPrefix(rawURL, "file://") || strings.HasPrefix(rawURL, "data:") {
				log.Printf("generation: persist local output failed task=%d idx=%d: %v", taskID, i, err)
				return nil, nil, nil, fmt.Errorf("%w: %v", errLocalPersist, err)
			}
			// 抓不到(未知 scheme / 超时 / 上游 4xx)或传不上:保留原始 URL、assetId 记 0,
			// 任务照常成功——用户看到的仍是真实生成的图。日志必须留:曾经这里全静默,
			// 线上 87 条泄漏一条痕迹都没有。已自动重试 3 次;之后由用户在节点上点「重试」走 RepersistTask（spec S5）。
			log.Printf("generation: persist output failed task=%d idx=%d url=%s: %v (kept upstream url)", taskID, i, rawURL, err)
			out = append(out, rawURL)
			assetIDs = append(assetIDs, 0)
			mimes = append(mimes, "")
			continue
		}
		out = append(out, stored)
		assetIDs = append(assetIDs, assetID)
		mimes = append(mimes, mime)
	}
	return out, assetIDs, mimes, nil
}

// persistSlot 把一个产物槽位从上游搬进对象存储:下载 → 确定性 key 上传 → assets 行 +
// 用户存储用量同事务。persistOutputs(首次)与 repersistTask(手动补搬)共用这一段,
// 两条路写出的 key / URL / asset 行逐字同形——补搬不是第二套逻辑。
// 返回 canonical URL(media://<key>)、asset id、mime。
func (s *Service) persistSlot(ctx context.Context, userID, taskID, projectID int64, i int, rawURL string, durationMs int64) (string, int64, string, error) {
	src, size, mime, err := s.openSlotSource(ctx, rawURL)
	if err != nil {
		return "", 0, "", err
	}
	defer src.Close()
	// key 只由 taskID/outputIndex 确定(不含 ext——ext 从本次 mime 推导,重放时
	// mime 变化会导致 key 漂移;MIME 类型存对象 Content-Type)。不走含年月的
	// NewKey——重放同槽位覆盖写同一个 key,无孤儿对象。
	key := s.storage.StaticKey(userID, fmt.Sprintf("gen/task-%d-%d", taskID, i))
	stored, err := s.storage.PutObject(ctx, assets.PutObjectInput{
		Key:         key,
		ContentType: mime,
		Reader:      src,
		Size:        size,
	})
	if err != nil {
		return "", 0, "", fmt.Errorf("put object: %w", err)
	}
	var assetID int64
	if s.assets != nil && s.db != nil {
		// asset 行与存储用量同一事务(v5 §5.5):Insert 成功 AddStorage 失败的
		// 「永久少计存储」窗口随事务消失;唯一键兜底并发重放,只有真正新插入才计量。
		err := func() error {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback() }()
			var slotDuration int64
			if strings.HasPrefix(mime, "video/") {
				slotDuration = durationMs // 生成参数时长;非 video 保持 0 → NULL
			}
			id, inserted, err := s.assets.InsertSlotTx(tx, assets.AssetRecord{
				UserID:       userID,
				ProjectID:    projectID,
				SourceTaskID: taskID,
				ObjectKey:    key,
				SourceKind:   "generation",
				URL:          stored.URL,
				SizeBytes:    stored.Size,
				MimeType:     mime,
				DurationMs:   slotDuration,
			}, i)
			if err != nil {
				return err
			}
			if inserted && s.users != nil && stored.Size > 0 {
				if err := s.users.AddStorageTx(tx, userID, stored.Size); err != nil {
					return err
				}
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			assetID = id
			return nil
		}()
		if err != nil {
			log.Printf("generation: persist asset slot failed task=%d idx=%d: %v", taskID, i, err)
		}
	}
	return stored.URL, assetID, mime, nil
}

// openSlotSource 按引用形态给出产物内容流。file:// 是 gpt-image b64 落盘的临时文件，
// 直接打开、Stat 取长度、读 512 字节探测 mime 后 Seek 回头；http(s)/data: 走原来的
// fetchMedia（它们本来就整份进内存,形态没变）。
func (s *Service) openSlotSource(ctx context.Context, rawURL string) (io.ReadCloser, int64, string, error) {
	if !strings.HasPrefix(rawURL, "file://") {
		body, mime, _, err := s.fetchMedia(ctx, rawURL)
		if err != nil {
			return nil, 0, "", err
		}
		return io.NopCloser(bytes.NewReader(body)), int64(len(body)), mime, nil
	}
	path, err := s.resolveSpillOutputPath(strings.TrimPrefix(rawURL, "file://"))
	if err != nil {
		return nil, 0, "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, "", fmt.Errorf("open local output: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, "", err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, 0, "", err
	}
	mime := http.DetectContentType(head[:n])
	if idx := strings.Index(mime, ";"); idx >= 0 {
		mime = strings.TrimSpace(mime[:idx])
	}
	return f, st.Size(), mime, nil
}

func (s *Service) fetchMedia(ctx context.Context, rawURL string) ([]byte, string, string, error) {
	if strings.HasPrefix(rawURL, "data:") {
		return decodeDataURL(rawURL)
	}
	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, "", "", err
		}
		resp, err := s.httpClient.Do(req)
		if err != nil {
			return nil, "", "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, "", "", fmt.Errorf("fetch %d", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, "", "", err
		}
		mime := resp.Header.Get("Content-Type")
		if mime == "" {
			mime = "application/octet-stream"
		}
		return body, mime, extFromMime(mime), nil
	}
	return nil, "", "", fmt.Errorf("unsupported url scheme")
}

func decodeDataURL(dataURL string) ([]byte, string, string, error) {
	// 格式：data:[<mime>][;base64],<data>
	if !strings.HasPrefix(dataURL, "data:") {
		return nil, "", "", fmt.Errorf("not a data url")
	}
	rest := dataURL[5:]
	commaIdx := strings.Index(rest, ",")
	if commaIdx < 0 {
		return nil, "", "", fmt.Errorf("malformed data url")
	}
	header := rest[:commaIdx]
	payload := rest[commaIdx+1:]
	mime := "application/octet-stream"
	isBase64 := false
	for _, seg := range strings.Split(header, ";") {
		seg = strings.TrimSpace(seg)
		if seg == "base64" {
			isBase64 = true
		} else if seg != "" {
			mime = seg
		}
	}
	var body []byte
	var err error
	if isBase64 {
		body, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			body, err = base64.URLEncoding.DecodeString(payload)
		}
	} else {
		body = []byte(payload)
	}
	if err != nil {
		return nil, "", "", fmt.Errorf("decode base64: %w", err)
	}
	return body, mime, extFromMime(mime), nil
}

func extFromMime(mime string) string {
	// 去掉任何 ;charset=... 段
	if idx := strings.Index(mime, ";"); idx >= 0 {
		mime = strings.TrimSpace(mime[:idx])
	}
	switch mime {
	case "image/png":
		return "png"
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "video/mp4":
		return "mp4"
	case "video/webm":
		return "webm"
	case "video/quicktime":
		return "mov"
	case "audio/mpeg":
		return "mp3"
	case "audio/wav":
		return "wav"
	// OpenAI /v1/audio/speech 的 response_format 全家（mp3/wav 上面已有）。
	case "audio/opus", "audio/ogg":
		return "ogg"
	case "audio/aac":
		return "aac"
	case "audio/flac":
		return "flac"
	case "audio/mp4":
		return "m4a"
	}
	return "bin"
}
