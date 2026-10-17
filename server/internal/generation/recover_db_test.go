package generation

import (
	"database/sql"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

// newRecoverDBHarness：真实 SQLite + 记录计费调用的替身。本地用户 id=1 由迁移预置。
// leaseStaleSec 取 60（而不是 1）：「心跳新鲜」用例与断言之间跨过 1 秒边界就会误判，60 秒足够稳定。
func newRecoverDBHarness(t *testing.T) (*sql.DB, *Service, int64) {
	t.Helper()
	conn := dbtest.Open(t)
	svc := &Service{
		repo:          NewSQLiteRepository(conn),
		billing:       &billingSpy{},
		db:            conn,
		leaseStaleSec: 60,
	}
	return conn, svc, 1
}

// insertStaleRunning 插一条心跳早已超时的 running 行。
func insertStaleRunning(t *testing.T, conn *sql.DB, uid int64, sink, upstreamID string, called bool) int64 {
	t.Helper()
	var sinkVal, upVal, calledVal any
	if sink != "" {
		sinkVal = sink
	}
	if upstreamID != "" {
		upVal = upstreamID
	}
	if called {
		calledVal = "2026-01-01 00:00:00"
	}
	res, err := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, upstream_task_id, upstream_called_at, owner_instance, heartbeat_at, lease_version)
		VALUES (?, 'm-rc', 'running', '{}', 40, ?, ?, ?, 'dead-instance', datetime('now','-1 hours'), 3)`,
		uid, sinkVal, upVal, calledVal)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func taskState(t *testing.T, conn *sql.DB, id int64) (status string, lease int64) {
	t.Helper()
	if err := conn.QueryRow(`SELECT status, lease_version FROM generation_tasks WHERE id=?`, id).Scan(&status, &lease); err != nil {
		t.Fatalf("read task: %v", err)
	}
	return
}

func assertOps(t *testing.T, svc *Service, want ...string) {
	t.Helper()
	ops := spyOf(svc).ops()
	if len(ops) != len(want) {
		t.Fatalf("计费调用 %v，want %v", ops, want)
	}
	for i, op := range ops {
		if op.Op != want[i] {
			t.Fatalf("计费调用 %v，want %v", ops, want)
		}
	}
}

// 行 3:没调过上游 + 有 sink(队列任务) → 接管后重排,不碰计费。
func TestTakeoverRequeuesUncalledQueueTask(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	id := insertStaleRunning(t, conn, uid, `{"kind":"canvas_node"}`, "", false)

	svc.RecoverStaleQueueTasks()

	status, lease := taskState(t, conn, id)
	if status != "queued" {
		t.Fatalf("应重排回 queued, got %s", status)
	}
	if lease <= 3 {
		t.Fatalf("接管+重排必须推进代次, got %d", lease)
	}
	assertOps(t, svc)
}

// 行 4:没调过上游 + 无 sink(直连孤儿) → 原子失败收口。
func TestTakeoverFailsSinklessDirectOrphanWithRefund(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	id := insertStaleRunning(t, conn, uid, "", "", false)

	svc.RecoverStaleQueueTasks()

	if status, _ := taskState(t, conn, id); status != "failed" {
		t.Fatalf("直连孤儿应失败, got %s", status)
	}
	assertOps(t, svc, "RefundTx")
}

// 行 2:调过上游但没拿到 id → 结论不确定,保守失败,且有 sink 必须插 outbox 通知画布。
func TestTakeoverRefundsWhenUpstreamOutcomeUnknown(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	id := insertStaleRunning(t, conn, uid, `{"kind":"canvas_node"}`, "", true)

	svc.RecoverStaleQueueTasks()

	if status, _ := taskState(t, conn, id); status != "failed" {
		t.Fatalf("不确定结论应保守失败, got %s", status)
	}
	assertOps(t, svc, "RefundTx")
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_sink_outbox WHERE task_id=?`, id).Scan(&n)
	if n != 1 {
		t.Fatalf("失败收口应插 outbox 通知发起方, got %d 行", n)
	}
}

// 行 1:有 upstream id → 本接管器不插手(续 poll 归 Resume)。
func TestTakeoverLeavesResumableTasksAlone(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	id := insertStaleRunning(t, conn, uid, `{"kind":"canvas_node"}`, "up-123", true)

	svc.RecoverStaleQueueTasks()

	status, lease := taskState(t, conn, id)
	if status != "running" || lease != 3 {
		t.Fatalf("可续 poll 的任务不该被动, got %s lease=%d", status, lease)
	}
	assertOps(t, svc)
}

// 心跳新鲜的任务谁也不许碰——接管 CAS 抢不到。
func TestTakeoverSkipsFreshHeartbeat(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	res, _ := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, owner_instance, heartbeat_at, lease_version)
		VALUES (?, 'm-rc', 'running', '{}', 40, '{"kind":"canvas_node"}', 'alive', CURRENT_TIMESTAMP, 3)`, uid)
	id, _ := res.LastInsertId()

	svc.RecoverStaleQueueTasks()

	status, lease := taskState(t, conn, id)
	if status != "running" || lease != 3 {
		t.Fatalf("心跳新鲜的任务不许接管, got %s lease=%d", status, lease)
	}
}

// 排队超时:queued 行经 finishQueuedTimeoutTx 原子失败(CAS 内 lease+1 防与领取竞态)。
func TestSweepStuckQueuedAtomicRefund(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	res, _ := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, created_at)
		VALUES (?, 'm-rc', 'queued', '{}', 40, '{"kind":"canvas_node"}', datetime('now','-7 hours'))`, uid)
	id, _ := res.LastInsertId()

	svc.SweepStuckQueued()

	status, lease := taskState(t, conn, id)
	if status != "failed" || lease == 0 {
		t.Fatalf("排队超时应失败且推进代次, got %s lease=%d", status, lease)
	}
	assertOps(t, svc, "RefundTx")
}
