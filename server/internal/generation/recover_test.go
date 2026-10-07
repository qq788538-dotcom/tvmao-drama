package generation

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
)

// fakeRecoverRepo 只实现 RecoverStaleSyncTasks 用到的方法。
// Repository 其余方法留空实现——测试不走那些路径。
type fakeRecoverRepo struct {
	stale      []Task
	markedFail []int64
}

func (f *fakeRecoverRepo) MarkRunningDirectTx(*sql.Tx, int64, string) error { return nil }
func (f *fakeRecoverRepo) CreateTx(*sql.Tx, Task) (int64, error)            { return 0, nil }
func (f *fakeRecoverRepo) MarkRetryPending(int64, int64, int, int, string) (bool, error) {
	return false, nil
}
func (f *fakeRecoverRepo) PromoteDueRetries(int) (int64, error) { return 0, nil }
func (f *fakeRecoverRepo) StatusLiteByIDs([]int64) (map[int64]TaskStatusLite, error) {
	return map[int64]TaskStatusLite{}, nil
}

// 未使用的 Repository 方法——空实现满足接口，测试不调用。
func (f *fakeRecoverRepo) Create(task Task) (int64, error)         { return 0, nil }
func (f *fakeRecoverRepo) Get(userID, taskID int64) (*Task, error) { return nil, nil }
func (f *fakeRecoverRepo) LatestByCanvasNode(int64, int64, string) (*Task, error) {
	return nil, ErrNotFound
}
func (f *fakeRecoverRepo) List(filter ListFilter) ([]Task, int64, error)   { return nil, 0, nil }
func (f *fakeRecoverRepo) DeleteTx(*sql.Tx, int64, int64) error            { return nil }
func (f *fakeRecoverRepo) DeleteForProject(projectID int64) error          { return nil }
func (f *fakeRecoverRepo) MarkUpstream(int64, int64, string) (bool, error) { return true, nil }

func (f *fakeRecoverRepo) ListProcessing(maxAgeSec, staleSec int) ([]Task, error) { return nil, nil }

// 队列方法在 recover 测试里用不到，给最小 stub；队列逻辑由 queue_worker_test.go 的
// 专用 fake 充分覆盖。
func (f *fakeRecoverRepo) RunningCountByModel(int) (map[string]int, error) { return nil, nil }
func (f *fakeRecoverRepo) ListQueuedForModel(string, int) ([]Task, error)  { return nil, nil }
func (f *fakeRecoverRepo) ClaimQueued(int64, string) (int64, bool, error)  { return 0, false, nil }
func (f *fakeRecoverRepo) ClaimStaleRunning(int64, string, int) (int64, bool, error) {
	return 0, false, nil
}
func (f *fakeRecoverRepo) MarkUpstreamCalled(int64, int64, string) (bool, error) { return true, nil }
func (f *fakeRecoverRepo) RequeueStale(int64, int64) (bool, error)               { return false, nil }
func (f *fakeRecoverRepo) ListStaleRunning(int) ([]Task, error)                  { return nil, nil }
func (f *fakeRecoverRepo) ListQueuedOlderThan(int, int) ([]Task, error)          { return nil, nil }
func (f *fakeRecoverRepo) QueuedModels() ([]string, error)                       { return nil, nil }

func (f *fakeRecoverRepo) ListQueuedSinkless(int, int) ([]Task, error) { return nil, nil }
func (f *fakeRecoverRepo) SinkForTask(int64) (json.RawMessage, error)  { return nil, nil }
func (f *fakeRecoverRepo) Heartbeat(int64, int64) (bool, error)        { return true, nil }

// fakeRefunder 记录所有退款调用，供断言验证。实现完整 Billing 接口，
// Freeze/Settle 空实现，测试只走 Refund 路径。
type fakeRefunder struct {
	refunds []struct {
		taskID int64
		amount int64
	}
}

func (f *fakeRefunder) Freeze(ctx context.Context, userID, taskID, amountCredits int64, note string) (BillingResult, error) {
	return BillingResult{}, nil
}
func (f *fakeRefunder) Settle(ctx context.Context, userID, taskID, frozenCredits, actualCredits int64, note string) (BillingResult, error) {
	return BillingResult{}, nil
}
func (f *fakeRefunder) FreezeTx(ctx context.Context, tx *sql.Tx, userID, taskID, amountCredits int64, note string) (BillingResult, error) {
	return BillingResult{}, nil
}
func (f *fakeRefunder) SettleTx(ctx context.Context, tx *sql.Tx, userID, taskID, frozenCredits, actualCredits int64, note string) (BillingResult, error) {
	return BillingResult{}, nil
}
func (f *fakeRefunder) RefundTx(ctx context.Context, tx *sql.Tx, userID, taskID, frozenCredits int64, note string) (BillingResult, error) {
	return BillingResult{}, nil
}
func (f *fakeRefunder) Refund(ctx context.Context, userID, taskID, amount int64, note string) (BillingResult, error) {
	f.refunds = append(f.refunds, struct {
		taskID int64
		amount int64
	}{taskID, amount})
	return BillingResult{}, nil
}

// newRecoverServiceForTest 构造一个只填了 RecoverStaleSyncTasks 所需字段的 Service。
// 其余字段为零值——测试不走那些路径，不会 panic。
func newRecoverServiceForTest(repo Repository, refunder Billing) *Service {
	return &Service{
		repo:          repo,
		billing:       refunder,
		leaseStaleSec: 90,
	}
}

// ── 心跳失租语义(v5 §5.3):影响 0 行必须触发 onLost ─────────────────────

type heartbeatSpyRepo struct {
	fakeRecoverRepo
	won bool
}

func (f *heartbeatSpyRepo) Heartbeat(int64, int64) (bool, error) { return f.won, nil }

func TestStartHeartbeatStopsIdempotently(t *testing.T) {
	repo := &heartbeatSpyRepo{won: true}
	s := &Service{repo: repo}
	stop := s.startHeartbeat(42, 7, func() {})
	// 停两次不能 panic——defer 与显式调用可能同时发生。
	stop()
	stop()
}
