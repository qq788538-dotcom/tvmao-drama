package generation

import (
	"database/sql"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

// v5 §5.6/§8.2:outbox fencing 的真实 DB 验证。
// 关键时序:worker A 领取 → 租约过期 → worker B 接管(代次+1) → A 带旧代次写回
// → 必须影响 0 行——A 既不能把行标 done,也不能覆盖 B 的重试状态。

func insertOutboxRow(t *testing.T, conn *sql.DB) int64 {
	t.Helper()
	res, err := conn.Exec(`INSERT INTO generation_sink_outbox (task_id, sink, payload)
		VALUES (?, '{"kind":"canvas_node","projectId":1,"nodeId":"n"}', '{"outputUrls":["u"]}')`,
		insertOutboxSeq(t, conn))
	if err != nil {
		t.Fatalf("insert outbox: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

var outboxTaskSeq int64 = 90000

func insertOutboxSeq(t *testing.T, conn *sql.DB) int64 {
	outboxTaskSeq++
	return outboxTaskSeq
}

func TestOutboxClaimAndComplete(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	id := insertOutboxRow(t, conn)

	batch, err := repo.claimOutboxDue(10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	var row *outboxRow
	for i := range batch {
		if batch[i].ID == id {
			row = &batch[i]
		}
	}
	if row == nil {
		t.Fatalf("应领到刚插入的行, batch=%v", batch)
	}
	if row.DeliveryVersion != 1 || row.Attempt != 1 {
		t.Fatalf("首领代次应为 1/尝试 1, got v=%d a=%d", row.DeliveryVersion, row.Attempt)
	}
	won, err := repo.completeOutbox(id, row.DeliveryVersion)
	if err != nil || !won {
		t.Fatalf("持有效代次写回应成功, won=%v err=%v", won, err)
	}
	var state string
	_ = conn.QueryRow(`SELECT state FROM generation_sink_outbox WHERE id=?`, id).Scan(&state)
	if state != "done" {
		t.Fatalf("state=%s, want done", state)
	}
}

func TestOutboxFencingStaleWorkerCannotWrite(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	id := insertOutboxRow(t, conn)

	// worker A 领取
	batchA, _ := repo.claimOutboxDue(10)
	var verA int64
	for _, r := range batchA {
		if r.ID == id {
			verA = r.DeliveryVersion
		}
	}
	if verA == 0 {
		t.Fatal("A 未领到")
	}
	// 租约人工过期 → worker B 接管
	if _, err := conn.Exec(`UPDATE generation_sink_outbox SET locked_until = datetime('now','-1 seconds') WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	batchB, _ := repo.claimOutboxDue(10)
	var verB int64
	for _, r := range batchB {
		if r.ID == id {
			verB = r.DeliveryVersion
		}
	}
	if verB != verA+1 {
		t.Fatalf("B 接管代次应为 A+1, got A=%d B=%d", verA, verB)
	}

	// A 带旧代次写 done → 必须落空
	if won, _ := repo.completeOutbox(id, verA); won {
		t.Fatal("失租的 A 绝不能把行标 done")
	}
	// A 带旧代次写失败重试 → 同样落空
	if won, _ := repo.failOutbox(id, verA, 1, "stale"); won {
		t.Fatal("失租的 A 绝不能覆盖 B 的状态")
	}
	// B 正常收尾
	if won, _ := repo.completeOutbox(id, verB); !won {
		t.Fatal("B 持有效代次应能写回")
	}
}

func TestOutboxFailBackoffThenDead(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	id := insertOutboxRow(t, conn)

	batch, _ := repo.claimOutboxDue(10)
	var row *outboxRow
	for i := range batch {
		if batch[i].ID == id {
			row = &batch[i]
		}
	}
	if row == nil {
		t.Fatal("未领到")
	}
	// 未超上限 → 退避回 pending
	if won, _ := repo.failOutbox(id, row.DeliveryVersion, row.Attempt, "第一次失败"); !won {
		t.Fatal("失败写回应成功")
	}
	var state string
	var nextRetry any
	_ = conn.QueryRow(`SELECT state, next_retry_at FROM generation_sink_outbox WHERE id=?`, id).Scan(&state, &nextRetry)
	if state != "pending" || nextRetry == nil {
		t.Fatalf("应回 pending 且带退避时刻, got state=%s next=%v", state, nextRetry)
	}

	// 伪造尝试数达上限后再失败 → dead
	if _, err := conn.Exec(`UPDATE generation_sink_outbox SET next_retry_at=CURRENT_TIMESTAMP WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	batch2, _ := repo.claimOutboxDue(10)
	var row2 *outboxRow
	for i := range batch2 {
		if batch2[i].ID == id {
			row2 = &batch2[i]
		}
	}
	if row2 == nil {
		t.Fatal("退避到点后应能再领")
	}
	if won, _ := repo.failOutbox(id, row2.DeliveryVersion, outboxMaxAttempt, "持续失败"); !won {
		t.Fatal("dead 写回应成功")
	}
	_ = conn.QueryRow(`SELECT state FROM generation_sink_outbox WHERE id=?`, id).Scan(&state)
	if state != "dead" {
		t.Fatalf("超上限应置 dead, got %s", state)
	}
}

// B7 回归(codex 复核建议):pending 但退避未到点的行,连领取 CAS 也必须打不中——
// 即使它的 id 被并发 worker 预读进候选集。
func TestOutboxFutureRetryNotClaimable(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	id := insertOutboxRow(t, conn)
	if _, err := conn.Exec(`UPDATE generation_sink_outbox SET next_retry_at = datetime('now','+1 hours') WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	batch, err := repo.claimOutboxDue(100)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	for _, r := range batch {
		if r.ID == id {
			t.Fatal("退避未到点的行绝不能被领取")
		}
	}
	var attempt int
	_ = conn.QueryRow(`SELECT attempt FROM generation_sink_outbox WHERE id=?`, id).Scan(&attempt)
	if attempt != 0 {
		t.Fatalf("attempt 不该被消耗, got %d", attempt)
	}
}
