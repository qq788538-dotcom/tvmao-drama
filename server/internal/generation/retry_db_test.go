package generation_test

import (
	"database/sql"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/generation"
)

// v5 §4.2 重试协议的真实 DB 验证:
//   - MarkRetryPending 单条 UPDATE 同时完成判定(状态 CAS + sink 非空 + attempt<上限)
//     与转移(attempt+1、清上游标记、清租约);
//   - sinkless / 上限耗尽 抢不到;
//   - PromoteDueRetries 只放行到点的;
//   - 整个重试循环钱一个字不动——「重试 N 次只有一笔冻结」是结构性的。

func insertRetryTask(t *testing.T, conn *sql.DB, status string, sink string, attempt int) int64 {
	t.Helper()
	var sinkVal any
	if sink != "" {
		sinkVal = sink
	}
	res, err := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, attempt, sink, upstream_task_id, upstream_called_at, owner_instance, heartbeat_at, lease_version)
		VALUES (1, 'm-r', ?, '{}', 42, ?, ?, 'up-1', CURRENT_TIMESTAMP, 'inst-a', CURRENT_TIMESTAMP, 5)`, status, attempt, sinkVal)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestMarkRetryPendingTransitionsAndClearsMarkers(t *testing.T) {
	conn := dbtest.Open(t)
	repo := generation.NewSQLiteRepository(conn)
	id := insertRetryTask(t, conn, "processing", `{"kind":"canvas_node","projectId":1,"nodeId":"n"}`, 0)

	won, err := repo.MarkRetryPending(id, 5, 2, 30, "上游宣告失败")
	if err != nil || !won {
		t.Fatalf("应抢到重试, won=%v err=%v", won, err)
	}
	var status, errMsg string
	var attempt int
	var upID, called, owner, hb any
	if err := conn.QueryRow(`SELECT status, attempt, IFNULL(error,''), upstream_task_id, upstream_called_at, owner_instance, heartbeat_at
		FROM generation_tasks WHERE id=?`, id).Scan(&status, &attempt, &errMsg, &upID, &called, &owner, &hb); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != "retry_pending" || attempt != 1 || errMsg != "上游宣告失败" {
		t.Fatalf("转移后 status=%s attempt=%d err=%q", status, attempt, errMsg)
	}
	if upID != nil || called != nil || owner != nil || hb != nil {
		t.Fatalf("上游标记与租约必须清空, got upID=%v called=%v owner=%v hb=%v", upID, called, owner, hb)
	}
	// 钱没动:frozen_cents 原样(结构性——该 UPDATE 根本不含资金列)。
	var frozen int64
	_ = conn.QueryRow(`SELECT frozen_cents FROM generation_tasks WHERE id=?`, id).Scan(&frozen)
	if frozen != 42 {
		t.Fatalf("重试不动钱, frozen=%d", frozen)
	}
}

func TestMarkRetryPendingRefusesSinklessAndExhausted(t *testing.T) {
	conn := dbtest.Open(t)
	repo := generation.NewSQLiteRepository(conn)

	sinkless := insertRetryTask(t, conn, "processing", "", 0)
	if won, _ := repo.MarkRetryPending(sinkless, 5, 5, 30, "x"); won {
		t.Fatal("sinkless 绝不能进重试——没有 worker 会替同步调用方交付结果")
	}
	exhausted := insertRetryTask(t, conn, "processing", `{"kind":"drama_candidate"}`, 2)
	if won, _ := repo.MarkRetryPending(exhausted, 5, 2, 30, "x"); won {
		t.Fatal("attempt 已达上限不能再排")
	}
	terminal := insertRetryTask(t, conn, "failed", `{"kind":"drama_candidate"}`, 0)
	if won, _ := repo.MarkRetryPending(terminal, 5, 2, 30, "x"); won {
		t.Fatal("终态任务不能被拉回重试")
	}
}

// 失租者带旧代次排重试必须落空——重试转移也在 fencing 保护内。
func TestMarkRetryPendingStaleLeaseLosesRace(t *testing.T) {
	conn := dbtest.Open(t)
	repo := generation.NewSQLiteRepository(conn)
	id := insertRetryTask(t, conn, "processing", `{"kind":"canvas_node"}`, 0)
	if won, _ := repo.MarkRetryPending(id, 4, 3, 30, "旧代次"); won {
		t.Fatal("旧代次(4≠5)绝不能完成重试转移")
	}
}

func TestPromoteDueRetriesOnlyDue(t *testing.T) {
	conn := dbtest.Open(t)
	repo := generation.NewSQLiteRepository(conn)

	due := insertRetryTask(t, conn, "processing", `{"kind":"canvas_node"}`, 0)
	notDue := insertRetryTask(t, conn, "processing", `{"kind":"canvas_node"}`, 0)
	if won, _ := repo.MarkRetryPending(due, 5, 3, 30, "e"); !won {
		t.Fatal("due 应转入 retry_pending")
	}
	if won, _ := repo.MarkRetryPending(notDue, 5, 3, 3600, "e"); !won {
		t.Fatal("notDue 应转入 retry_pending")
	}
	// 把 due 的到点时刻拨到过去。
	if _, err := conn.Exec(`UPDATE generation_tasks SET next_retry_at = datetime('now','-1 seconds') WHERE id=?`, due); err != nil {
		t.Fatalf("rewind: %v", err)
	}

	if _, err := repo.PromoteDueRetries(100); err != nil {
		t.Fatalf("promote: %v", err)
	}
	var st1, st2 string
	_ = conn.QueryRow(`SELECT status FROM generation_tasks WHERE id=?`, due).Scan(&st1)
	_ = conn.QueryRow(`SELECT status FROM generation_tasks WHERE id=?`, notDue).Scan(&st2)
	if st1 != "queued" {
		t.Fatalf("到点的应回 queued, got %s", st1)
	}
	if st2 != "retry_pending" {
		t.Fatalf("没到点的必须原地等待, got %s", st2)
	}
}
