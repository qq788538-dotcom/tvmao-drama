package generation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

	// ── 持久化队列（0080）────────────────────────────────────────────
	// Sink 结果去向。generation 只认其中的 kind，其余字段原样交给注册的 handler。
	Sink json.RawMessage `json:"-"`
	// UpstreamCalledAt 首次向上游发出请求的时刻。被中断后能否安全重排全靠它：
	// nil=什么都没发生可原样重排；非 nil 但没拿到 UpstreamTaskID=不确定上游收没收，
	// 只能保守退款，绝不重复扣费。
	UpstreamCalledAt *time.Time `json:"-"`
	Attempt          int        `json:"-"` // 已尝试次数（含重排）；不影响计费
	// UpstreamFingerprint 提交时所用上游的指纹（0071，只有 ListProcessing 读）；空串=旧任务。
	UpstreamFingerprint string     `json:"-"`
	NextRetryAt         *time.Time `json:"-"`
}

var ErrNotFound = errors.New("task not found")

// ErrTaskNotTerminal：删除接口只允许删终态任务。非终态行持有冻结,
// 删了钱既不结算也不退款、对账永久报警——删除是清理历史的操作,不是取消任务的通道。
var ErrTaskNotTerminal = errors.New("task not terminal")

// ErrDataURIInput：sink 任务的输入含内联 data URI，Submit 拒绝（HTTP 层映射 400）。
// 产品路径全 URL，这是给绕过前端的直连调用方的明确报错，不是兜底。
var ErrDataURIInput = errors.New("参考图请先上传为素材：不支持内联图片数据(data URI)")

// sqlSecondsOffset 生成 datetime('now', ?) 的修饰串：-90 → "-90 seconds"，30 → "+30 seconds"。
// 替代 MySQL 的 DATE_SUB/DATE_ADD(NOW(), INTERVAL ? SECOND)；结果与 CURRENT_TIMESTAMP 同格式，可直接比较。
func sqlSecondsOffset(sec int) string { return fmt.Sprintf("%+d seconds", sec) }

type ListFilter struct {
	UserID    int64
	ProjectID *int64
	Status    string
	ModelID   string
	Kind      string // 产物类型过滤(image/video/audio);空=不过滤
	Search    string
	Limit     int
	Offset    int
}

type Repository interface {
	Create(task Task) (int64, error)
	Get(userID, taskID int64) (*Task, error)
	// LatestByCanvasNode 按 (user, project, sink.nodeId) 取该画布节点最新一次任务。
	// 前端节点丢 taskId 后的自愈对账入口。
	LatestByCanvasNode(userID, projectID int64, nodeID string) (*Task, error)
	List(filter ListFilter) ([]Task, int64, error)
	// DeleteTx 事务内删终态任务行;非终态 ErrTaskNotTerminal(见实现注释)。
	DeleteTx(tx *sql.Tx, userID, taskID int64) error
	DeleteForProject(projectID int64) error
	// MarkUpstream 落上游任务号并转 processing。带代次:失租者写不进。
	MarkUpstream(id, lease int64, upstreamTaskID string) (bool, error)
	// ListProcessing 返回卡在 'processing'/'settling' 状态、且带有 upstream task id 的任务，
	// 用于在 server 重启后恢复成孤儿的异步任务。传 0 表示不限时长。
	// staleSec：只取 owner_instance 为空、或 heartbeat 超过此秒数的行（跳过仍在跑的本实例任务）。
	ListProcessing(maxAgeSec, staleSec int) ([]Task, error)
	// Heartbeat 续租：仅当 owner_instance 匹配时更新 heartbeat_at（防止接管后被旧租约覆盖）。
	// Heartbeat 按代次续租;影响 0 行 = 失租,调用方必须立刻 cancel 执行 ctx。
	Heartbeat(taskID, lease int64) (bool, error)
	// StatusLiteByIDs 批量取任务的轻量状态（status/attempt/error），供业务层
	// List 时把重试进度投影到 DTO。ids 空返回空 map。
	StatusLiteByIDs(ids []int64) (map[int64]TaskStatusLite, error)
	// MarkRetryPending 把权威终态失败的任务转入 retry_pending（业务重试）：
	// attempt+1、清空上游标记（权威失败=上游宣告任务死亡，清空安全）、清租约。
	// 判定与转移原子：WHERE 带 sink 非空 + attempt<上限 + 状态 CAS，抢不到返回 false。
	// 钱一个字不碰——重试复用同一笔冻结。
	MarkRetryPending(taskID, lease int64, maxAttempts, intervalSec int, errMsg string) (bool, error)
	// PromoteDueRetries 把到点的 retry_pending 放回 queued（并入队列 pump，不新增 worker）。
	PromoteDueRetries(limit int) (int64, error)
	// CreateTx 在调用方事务里建行(SubmitTx 用)：与 Create 同一 INSERT，
	// 只是执行在 tx 上——行随事务原子出现，pending_freeze 中间态因此不需要。
	CreateTx(tx *sql.Tx, task Task) (int64, error)
	// MarkRunningDirectTx 在建行事务里给 sinkless 直连任务落执行权:owner+heartbeat+
	// lease=1(建行者即首任持有者)。直连任务全生命周期不经过 queued,worker 结构上
	// 领不到。
	MarkRunningDirectTx(tx *sql.Tx, taskID int64, instance string) error
	// ── 持久化队列（0080）────────────────────────────────────────────
	// RunningCountByModel 统计每个模型当前在跑的任务数（只算心跳新鲜的），
	// 供 worker 算每模型还有多少空位。并发上限按模型独立，绝不共用一把闸。
	RunningCountByModel(freshSec int) (map[string]int, error)
	// ListQueuedForModel 取某模型排队中的任务。必须按模型取而不是按 id 扫全队列——
	// 否则一个满员模型的任务会把后面所有其他模型的任务堵住（队头阻塞）。
	// 只取带 sink 的行：没有 sink 的是老语义任务，由旧 recover 路径收尾。
	ListQueuedForModel(modelID string, limit int) ([]Task, error)
	// ClaimQueued CAS 领取：仅当仍是 queued 才置 running 并写租约，跨实例安全。
	// ClaimQueued CAS 领取并返回新执行权代次(领取即持有)。
	ClaimQueued(taskID int64, instance string) (int64, bool, error)
	// ClaimStaleRunning 接管心跳超时的在跑任务并返回新代次。
	ClaimStaleRunning(taskID int64, instance string, staleSec int) (int64, bool, error)
	// MarkUpstreamCalled 在向上游发请求之前调用，落下"已调过上游"的事实。
	// fingerprint 是本次调用所用上游的指纹（models.UpstreamFingerprint），同一条 UPDATE 落库，
	// 重启续轮询时比对。
	MarkUpstreamCalled(taskID, lease int64, fingerprint string) (bool, error)
	// RequeueStale 把被中断且从未调过上游的任务放回队列（attempt+1，清租约）。
	// 不碰 frozen_cents——重排复用原来那一笔冻结，绝不重新冻结。
	RequeueStale(taskID, lease int64) (bool, error)
	// ListStaleRunning 取租约过期的 running 任务，供 leader 分流接管。
	ListStaleRunning(staleSec int) ([]Task, error)
	// ListQueuedSinkless 取「queued 但没有 sink」且滞留超时的任务。
	// 这类任务既进不了队列也够不着 SweepStuckQueued，必须单独兜底，
	// 否则冻结的钱永久卡住。
	ListQueuedSinkless(sec int, limit int) ([]Task, error)
	// SinkForTask 取任务的结果去向（通用 Get 不读这一列）。
	SinkForTask(taskID int64) (json.RawMessage, error)
	// QueuedModels 给出当前有排队任务的模型清单，worker 据此只对相关模型算空位。
	QueuedModels() ([]string, error)
	// ListQueuedOlderThan 取排队超过指定时长仍没被领取的任务，供兜底超时退款。
	// 没有这道兜底，模型被删/永远排不上的任务会永久卡住、冻结的钱退不回来。
	ListQueuedOlderThan(sec int, limit int) ([]Task, error)
}

type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

func (r *SQLiteRepository) Create(task Task) (int64, error) {
	var projectID any
	if task.ProjectID != nil {
		projectID = *task.ProjectID
	}
	// sink 必须一起写进去：队列只领 sink IS NOT NULL 的行，漏写会让任务永远排不上，
	// 而且连 SweepStuckQueued 也够不着它（那条兜底同样要求 sink 非空），冻结的钱
	// 会永久卡住。
	var sink any
	if len(task.Sink) > 0 {
		sink = string(task.Sink)
	}
	res, err := r.db.Exec(
		`INSERT INTO generation_tasks (user_id, project_id, model_id, status, input, frozen_cents, sink) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		task.UserID, projectID, task.ModelID, task.Status, string(task.Input), task.FrozenCents, sink,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CreateTx 见接口注释。列清单必须与 Create 完全一致——上一轮 Create 漏写 sink 列的
// 事故(见 sweepSinklessQueued)就是「同构 SQL 写两遍」的代价，改列时两处必须同步。
func (r *SQLiteRepository) CreateTx(tx *sql.Tx, task Task) (int64, error) {
	var projectID any
	if task.ProjectID != nil {
		projectID = *task.ProjectID
	}
	var sink any
	if len(task.Sink) > 0 {
		sink = string(task.Sink)
	}
	res, err := tx.Exec(
		`INSERT INTO generation_tasks (user_id, project_id, model_id, status, input, frozen_cents, sink) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		task.UserID, projectID, task.ModelID, task.Status, string(task.Input), task.FrozenCents, sink,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *SQLiteRepository) Get(userID, taskID int64) (*Task, error) {
	row := r.db.QueryRow(
		`SELECT id, user_id, project_id, model_id, status, IFNULL(upstream_task_id, ''), input, output, frozen_cents, cost_cents, IFNULL(audit_flag, ''), IFNULL(error, ''), created_at, updated_at FROM generation_tasks WHERE id = ? AND user_id = ?`,
		taskID, userID,
	)
	var t Task
	var projectID sql.NullInt64
	var output []byte
	var input []byte
	var cost sql.NullInt64
	if err := row.Scan(&t.ID, &t.UserID, &projectID, &t.ModelID, &t.Status, &t.UpstreamTaskID, &input, &output, &t.FrozenCents, &cost, &t.AuditFlag, &t.Error, &t.CreatedAt, &t.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t.Input = json.RawMessage(input)
	if projectID.Valid {
		v := projectID.Int64
		t.ProjectID = &v
	}
	if len(output) > 0 {
		t.Output = json.RawMessage(output)
	}
	if cost.Valid {
		v := cost.Int64
		t.CostCents = &v
	}
	return &t, nil
}

// LatestByCanvasNode 按 (user, project, sink.nodeId) 取该画布节点最新一次生成任务。
// 给「节点卡在 generating 但本地 taskId 丢失」的前端自愈对账用：找回任务号交还
// 现有轮询。user_id 条件即归属防线（IDOR）；sink 无索引，但查询恒带 project_id
// 且单项目任务量是百级，扫描成本可忽略——为这条低频兜底加虚拟列索引不值得。
func (r *SQLiteRepository) LatestByCanvasNode(userID, projectID int64, nodeID string) (*Task, error) {
	row := r.db.QueryRow(
		`SELECT id, user_id, project_id, model_id, status, IFNULL(upstream_task_id, ''), input, output, frozen_cents, cost_cents, IFNULL(audit_flag, ''), IFNULL(error, ''), created_at, updated_at
		 FROM generation_tasks
		 WHERE user_id = ? AND project_id = ?
		   AND json_extract(sink, '$.kind') = 'canvas_node' AND json_extract(sink, '$.nodeId') = ?
		 ORDER BY id DESC LIMIT 1`,
		userID, projectID, nodeID,
	)
	var t Task
	var pid sql.NullInt64
	var output []byte
	var input []byte
	var cost sql.NullInt64
	if err := row.Scan(&t.ID, &t.UserID, &pid, &t.ModelID, &t.Status, &t.UpstreamTaskID, &input, &output, &t.FrozenCents, &cost, &t.AuditFlag, &t.Error, &t.CreatedAt, &t.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t.Input = json.RawMessage(input)
	if pid.Valid {
		v := pid.Int64
		t.ProjectID = &v
	}
	if len(output) > 0 {
		t.Output = json.RawMessage(output)
	}
	if cost.Valid {
		v := cost.Int64
		t.CostCents = &v
	}
	return &t, nil
}

func (r *SQLiteRepository) List(f ListFilter) ([]Task, int64, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	conds := []string{"user_id = ?"}
	args := []any{f.UserID}
	if f.ProjectID != nil {
		conds = append(conds, "project_id = ?")
		args = append(args, *f.ProjectID)
	}
	if f.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, f.Status)
	}
	if f.ModelID != "" {
		conds = append(conds, "model_id = ?")
		args = append(args, f.ModelID)
	}
	if f.Kind != "" {
		conds = append(conds, "output_kind = ?")
		args = append(args, f.Kind)
	}
	if f.Search != "" {
		// JSON_SEARCH(input,'one',?) 的 SQLite 等价：递归遍历 input 里所有字符串值做 LIKE（不匹配键名）。
		conds = append(conds, "EXISTS (SELECT 1 FROM json_tree(generation_tasks.input) jt WHERE jt.type = 'text' AND jt.atom LIKE ?)")
		args = append(args, "%"+f.Search+"%")
	}
	where := strings.Join(conds, " AND ")

	var total int64
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// 两阶段取数：内层只对 id 排序分页，外层按主键取宽行。SQLite 下没有 sort buffer 问题，保留这种写法。
	q := `SELECT t.id, t.user_id, t.project_id, t.model_id, t.status, IFNULL(t.output_kind, ''), IFNULL(t.upstream_task_id, ''), t.input, t.output, t.frozen_cents, t.cost_cents, IFNULL(t.audit_flag, ''), IFNULL(t.error, ''), t.created_at, t.updated_at
		FROM generation_tasks t
		INNER JOIN (
			SELECT id FROM generation_tasks WHERE ` + where + ` ORDER BY id DESC LIMIT ? OFFSET ?
		) picked ON picked.id = t.id
		ORDER BY t.id DESC`
	args = append(args, f.Limit, f.Offset)
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Task, 0)
	for rows.Next() {
		var t Task
		var projectID sql.NullInt64
		var output []byte
		var input []byte
		var cost sql.NullInt64
		if err := rows.Scan(&t.ID, &t.UserID, &projectID, &t.ModelID, &t.Status, &t.OutputKind, &t.UpstreamTaskID, &input, &output, &t.FrozenCents, &cost, &t.AuditFlag, &t.Error, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, 0, err
		}
		t.Input = json.RawMessage(input)
		if projectID.Valid {
			v := projectID.Int64
			t.ProjectID = &v
		}
		if len(output) > 0 {
			t.Output = json.RawMessage(output)
		}
		if cost.Valid {
			v := cost.Int64
			t.CostCents = &v
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// DeleteTx 在调用方事务里删终态任务行:非终态行持有冻结,直删会让钱
// 悬挂、对账永久报警——返回 ErrTaskNotTerminal(handler 409)。
// 必须放在事务里:行删除必须与资产清理/orphan 标记/配额回收
// 同生共死,否则后半段失败后行已消失、重试 404、残留永久无兜底。
func (r *SQLiteRepository) DeleteTx(tx *sql.Tx, userID, taskID int64) error {
	res, err := tx.Exec(`DELETE FROM generation_tasks WHERE id = ? AND user_id = ? AND status IN ('succeeded','failed')`, taskID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM generation_tasks WHERE id=? AND user_id=?)`, taskID, userID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrTaskNotTerminal
	}
	return ErrNotFound
}

// DeleteForProject 清理项目历史,同样只删终态行——项目删除时若还有在途任务,
// 留给队列/接管器正常收口后再由孤儿清理兜底,不许直接抹掉持有冻结的行。
func (r *SQLiteRepository) DeleteForProject(projectID int64) error {
	_, err := r.db.Exec(`DELETE FROM generation_tasks WHERE project_id = ? AND status IN ('succeeded','failed')`, projectID)
	return err
}

// MarkUpstream 落上游任务号并转 processing。带代次:失租者写不进。
func (r *SQLiteRepository) MarkUpstream(id, lease int64, upstreamTaskID string) (bool, error) {
	res, err := r.db.Exec(`UPDATE generation_tasks SET upstream_task_id = ?, status = 'processing' WHERE id = ? AND lease_version = ?`, upstreamTaskID, id, lease)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *SQLiteRepository) ListProcessing(maxAgeSec, staleSec int) ([]Task, error) {
	// 纳入 settling：抢占结算后崩溃(MarkSucceeded 前)的任务停在 settling，resume 须能救回，否则冻结卡死。
	// staleSec 过滤：跳过仍有有效租约(heartbeat 新鲜)的任务，只接管无主或超时行。
	// running 也要纳入：队列任务被领取后是 running，若它已经拿到 upstream_task_id
	// 却因实例被杀而中断，只有这里能把它续 poll 回来。条件里的 upstream_task_id
	// 非空保证了不会误选"还没调上游"的任务(那类由 RecoverStaleQueueTasks 重排)。
	q := `SELECT id, user_id, project_id, model_id, status, IFNULL(upstream_task_id, ''), input, output, frozen_cents, cost_cents, IFNULL(audit_flag, ''), IFNULL(error, ''), created_at, updated_at, upstream_fingerprint FROM generation_tasks WHERE status IN ('running','processing','settling') AND upstream_task_id IS NOT NULL AND upstream_task_id <> ''`
	args := []any{}
	if maxAgeSec > 0 {
		q += ` AND created_at >= datetime('now', ?)`
		args = append(args, sqlSecondsOffset(-maxAgeSec))
	}
	// 只取无主、或 heartbeat 已超时的行；staleSec<=0 时退化为原行为（全取）。
	if staleSec > 0 {
		q += ` AND (owner_instance IS NULL OR heartbeat_at IS NULL OR heartbeat_at < datetime('now', ?))`
		args = append(args, sqlSecondsOffset(-staleSec))
	}
	q += ` ORDER BY id ASC`
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Task, 0)
	for rows.Next() {
		var t Task
		var projectID sql.NullInt64
		var output []byte
		var input []byte
		var cost sql.NullInt64
		if err := rows.Scan(&t.ID, &t.UserID, &projectID, &t.ModelID, &t.Status, &t.UpstreamTaskID, &input, &output, &t.FrozenCents, &cost, &t.AuditFlag, &t.Error, &t.CreatedAt, &t.UpdatedAt, &t.UpstreamFingerprint); err != nil {
			return nil, err
		}
		t.Input = json.RawMessage(input)
		if projectID.Valid {
			v := projectID.Int64
			t.ProjectID = &v
		}
		if len(output) > 0 {
			t.Output = json.RawMessage(output)
		}
		if cost.Valid {
			v := cost.Int64
			t.CostCents = &v
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Heartbeat 续租：仅当执行权代次匹配时更新 heartbeat_at。影响 0 行 = 失租——
// 调用方必须立刻 cancel 执行 ctx，尽早中止上游调用。
// (取得执行权统一走带代次的 ClaimQueued/ClaimStaleRunning,不存在无条件覆盖 owner 的入口。)
func (r *SQLiteRepository) Heartbeat(taskID, lease int64) (bool, error) {
	res, err := r.db.Exec(`UPDATE generation_tasks SET heartbeat_at=CURRENT_TIMESTAMP WHERE id=? AND lease_version=?`, taskID, lease)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, nil
	}
	// SQLite 的 RowsAffected 是匹配行数，同秒续租也返回 1；保留按代次复核作为失租的唯一判据。
	var cur int64
	if err := r.db.QueryRow(`SELECT lease_version FROM generation_tasks WHERE id=?`, taskID).Scan(&cur); err != nil {
		return false, err
	}
	return cur == lease, nil
}

// MarkRunningDirectTx 见接口注释。
func (r *SQLiteRepository) MarkRunningDirectTx(tx *sql.Tx, taskID int64, instance string) error {
	_, err := tx.Exec(`UPDATE generation_tasks
		SET owner_instance=?, heartbeat_at=CURRENT_TIMESTAMP, lease_version=1
		WHERE id=? AND status='running'`, instance, taskID)
	return err
}

// ── 持久化队列（0080）实现 ──────────────────────────────────────────────
//
// 队列的核心诉求：让「排队等待」这个状态活在 DB 里而不是某个进程的内存里，
// 这样任何实例都能接管，部署打断也不会丢任务、不会把钱永久冻在冻结态。

// queueTaskCols 是队列路径需要的列。比通用查询多了 sink / upstream_called_at /
// attempt，少了 output（排队和领取阶段用不上，避免白拉大字段）。
const queueTaskCols = `id, user_id, project_id, model_id, status, IFNULL(upstream_task_id, ''),
	input, frozen_cents, IFNULL(error, ''), sink, upstream_called_at, attempt, created_at, updated_at`

func scanQueueTask(rows interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var projectID sql.NullInt64
	var sink []byte
	var input []byte
	var calledAt sql.NullTime
	if err := rows.Scan(&t.ID, &t.UserID, &projectID, &t.ModelID, &t.Status, &t.UpstreamTaskID,
		&input, &t.FrozenCents, &t.Error, &sink, &calledAt, &t.Attempt, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return t, err
	}
	t.Input = json.RawMessage(input)
	if projectID.Valid {
		v := projectID.Int64
		t.ProjectID = &v
	}
	if len(sink) > 0 {
		t.Sink = json.RawMessage(sink)
	}
	if calledAt.Valid {
		v := calledAt.Time
		t.UpstreamCalledAt = &v
	}
	return t, nil
}

func (r *SQLiteRepository) RunningCountByModel(freshSec int) (map[string]int, error) {
	// 占槽的不只有 running：拿到上游 id 后任务转 processing 继续轮询，名额一直持有到
	// 收口(settling)为止。只数 running 会让其他实例看到虚假空位、继续领取，
	// 蓝绿双活期间实际并发能突破配置上限。
	rows, err := r.db.Query(`
		SELECT model_id, COUNT(*) FROM generation_tasks
		 WHERE status IN ('running','processing','settling') AND heartbeat_at IS NOT NULL
		   AND heartbeat_at > datetime('now', ?)
		 GROUP BY model_id`, sqlSecondsOffset(-freshSec))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var model string
		var n int
		if err := rows.Scan(&model, &n); err != nil {
			return nil, err
		}
		out[model] = n
	}
	return out, rows.Err()
}

// ListQueuedForModel 只取带 sink 的行：没有 sink 的是队列上线前的老任务，
// 语义不同（那时 queued 可能正在跑），交给旧 recover 路径收尾，别混进新队列。
func (r *SQLiteRepository) ListQueuedForModel(modelID string, limit int) ([]Task, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.db.Query(`SELECT `+queueTaskCols+` FROM generation_tasks
		 WHERE status = 'queued' AND model_id = ? AND sink IS NOT NULL
		 ORDER BY id ASC LIMIT ?`, modelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Task, 0, limit)
	for rows.Next() {
		t, err := scanQueueTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ClaimQueued CAS 领取。WHERE status='queued' 保证并发领取恰好一个成功；
// 新代次取自同一条语句的 RETURNING（替代 MySQL 的 LAST_INSERT_ID(expr)）。
func (r *SQLiteRepository) ClaimQueued(taskID int64, instance string) (int64, bool, error) {
	var lease int64
	err := r.db.QueryRow(`UPDATE generation_tasks
		SET status='running', owner_instance=?, heartbeat_at=CURRENT_TIMESTAMP,
		    lease_version = lease_version + 1
		WHERE id=? AND status='queued'
		RETURNING lease_version`, instance, taskID).Scan(&lease)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return lease, lease > 0, nil
}

// ClaimStaleRunning 接管一个心跳超时的 running/processing/settling 任务：CAS 抢占 + 代次+1，
// 旧执行者此后的一切写都会落空。
func (r *SQLiteRepository) ClaimStaleRunning(taskID int64, instance string, staleSec int) (int64, bool, error) {
	var lease int64
	err := r.db.QueryRow(`UPDATE generation_tasks
		SET owner_instance=?, heartbeat_at=CURRENT_TIMESTAMP,
		    lease_version = lease_version + 1
		WHERE id=? AND status IN ('running','processing','settling')
		  AND (owner_instance IS NULL OR heartbeat_at IS NULL OR heartbeat_at < datetime('now', ?))
		RETURNING lease_version`, instance, taskID, sqlSecondsOffset(-staleSec)).Scan(&lease)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return lease, lease > 0, nil
}

// MarkUpstreamCalled 必须在真正发出上游请求之前调用。之后即使进程被杀，接管方也能
// 从这个时间戳判断出「这次调用可能已经产生了外部副作用」，从而不敢重排、只敢退款。
// 带代次：影响 0 行 = 失租或状态异常，调用方必须中止，绝不能在标记不可靠时调上游。
func (r *SQLiteRepository) MarkUpstreamCalled(taskID, lease int64, fingerprint string) (bool, error) {
	res, err := r.db.Exec(
		`UPDATE generation_tasks SET upstream_called_at=CURRENT_TIMESTAMP, upstream_fingerprint=? WHERE id=? AND lease_version=? AND upstream_called_at IS NULL`,
		fingerprint, taskID, lease)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RequeueStale 放回队列。条件里带 upstream_called_at IS NULL 是最后一道保险：
// 万一调用方判断有误，DB 这一层也不会让「已经调过上游的任务」被重排出去重复扣费。
// 不递增 attempt：接管重排不是业务重试，部署中断不该消耗用户可见的
// 重试预算、也不该显示成「重试中(n)」；毒任务由排队超时按 created_at 总龄兜底。
func (r *SQLiteRepository) RequeueStale(taskID, lease int64) (bool, error) {
	res, err := r.db.Exec(`UPDATE generation_tasks
		SET status='queued', owner_instance=NULL, heartbeat_at=NULL, lease_version=lease_version+1
		WHERE id=? AND status='running' AND upstream_called_at IS NULL AND lease_version=?`, taskID, lease)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// TaskStatusLite 是投影用的轻量状态。
type TaskStatusLite struct {
	Status  string
	Attempt int
	Error   string
}

// StatusLiteByIDs 见接口注释。
func (r *SQLiteRepository) StatusLiteByIDs(ids []int64) (map[int64]TaskStatusLite, error) {
	out := make(map[int64]TaskStatusLite, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	q := "SELECT id, status, attempt, IFNULL(error,'') FROM generation_tasks WHERE id IN (?" + strings.Repeat(",?", len(ids)-1) + ")"
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var st TaskStatusLite
		if err := rows.Scan(&id, &st.Status, &st.Attempt, &st.Error); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// MarkRetryPending 见接口注释。清 upstream_task_id/upstream_called_at 只在这里合法：
// 前提是上游已权威宣告失败（调用方持 upstreamTerminalError 才会调进来）。
func (r *SQLiteRepository) MarkRetryPending(taskID, lease int64, maxAttempts, intervalSec int, errMsg string) (bool, error) {
	res, err := r.db.Exec(`UPDATE generation_tasks
		SET status='retry_pending', attempt=attempt+1,
		    next_retry_at=datetime('now', ?),
		    error=?, upstream_called_at=NULL, upstream_task_id=NULL,
		    owner_instance=NULL, heartbeat_at=NULL, lease_version=lease_version+1
		WHERE id=? AND lease_version=? AND status IN ('running','processing') AND sink IS NOT NULL AND attempt < ?`,
		sqlSecondsOffset(intervalSec), errMsg, taskID, lease, maxAttempts)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// PromoteDueRetries 见接口注释。lease_version+1：万一还有失租的旧执行者残留，
// 它带着旧代次什么也写不进（批次三接线校验后生效，这里先把代次推进备好）。
func (r *SQLiteRepository) PromoteDueRetries(limit int) (int64, error) {
	if limit <= 0 {
		limit = 200
	}
	// SQLite 不支持 UPDATE … LIMIT：先在子查询里按 id 选出这一批。
	res, err := r.db.Exec(`UPDATE generation_tasks
		SET status='queued', owner_instance=NULL, heartbeat_at=NULL, lease_version=lease_version+1
		WHERE id IN (
			SELECT id FROM generation_tasks
			 WHERE status='retry_pending' AND next_retry_at <= CURRENT_TIMESTAMP
			 ORDER BY id LIMIT ?)`, limit)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListStaleRunning 是统一接管器的输入：不按 sink 过滤——sinkless 直连
// 孤儿也必须被扫到，否则永久 running、冻结不释放。处置由调用方按状态表分流。
func (r *SQLiteRepository) ListStaleRunning(staleSec int) ([]Task, error) {
	rows, err := r.db.Query(`SELECT `+queueTaskCols+` FROM generation_tasks
		 WHERE status = 'running'
		   AND (heartbeat_at IS NULL OR heartbeat_at < datetime('now', ?))
		 ORDER BY id ASC`, sqlSecondsOffset(-staleSec))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Task, 0)
	for rows.Next() {
		t, err := scanQueueTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) ListQueuedOlderThan(sec int, limit int) ([]Task, error) {
	rows, err := r.db.Query(`SELECT `+queueTaskCols+` FROM generation_tasks
		 WHERE status = 'queued' AND sink IS NOT NULL
		   AND created_at < datetime('now', ?)
		 ORDER BY id ASC LIMIT ?`, sqlSecondsOffset(-sec), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Task, 0)
	for rows.Next() {
		t, err := scanQueueTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListQueuedSinkless 取 queued 但 sink 为空、且滞留超时的任务。见 sweepSinklessQueued。
func (r *SQLiteRepository) ListQueuedSinkless(sec int, limit int) ([]Task, error) {
	rows, err := r.db.Query(`SELECT `+queueTaskCols+` FROM generation_tasks
		 WHERE status = 'queued' AND sink IS NULL
		   AND created_at < datetime('now', ?)
		 ORDER BY id ASC LIMIT ?`, sqlSecondsOffset(-sec), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Task, 0)
	for rows.Next() {
		t, err := scanQueueTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// QueuedModels 给出当前有排队任务的模型清单。worker 只对这些模型算空位，
// 不用为全量模型做无谓查询。
func (r *SQLiteRepository) QueuedModels() ([]string, error) {
	rows, err := r.db.Query(
		`SELECT DISTINCT model_id FROM generation_tasks WHERE status='queued' AND sink IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, 4)
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SinkForTask 只取任务的结果去向。
// 单独一个查询而不是走通用 Get：通用查询的列清单里没有 sink，
// 从那条路径拿到的 Task.Sink 永远是空的——这正是"结果回写不了"最容易踩的坑。
func (r *SQLiteRepository) SinkForTask(taskID int64) (json.RawMessage, error) {
	var sink []byte
	err := r.db.QueryRow(`SELECT sink FROM generation_tasks WHERE id = ?`, taskID).Scan(&sink)
	if err != nil {
		return nil, err
	}
	if len(sink) == 0 {
		return nil, nil
	}
	return json.RawMessage(sink), nil
}

// UpdateOutput 整体替换 output JSON；outputKind 非空时同步 output_kind 列。只改仍是 succeeded 的行。
func (r *SQLiteRepository) UpdateOutput(taskID int64, output json.RawMessage, outputKind string) error {
	_, err := r.db.Exec(
		`UPDATE generation_tasks SET output=?, output_kind=COALESCE(NULLIF(?, ''), output_kind) WHERE id=? AND status='succeeded'`,
		string(output), outputKind, taskID)
	return err
}
