package generation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

// 针对 MySQL → SQLite 改写点的专项测试（方言清单里每一处改写至少被一个用例经过）。

func TestListSearchMatchesNestedInputStrings(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	for _, in := range []string{
		`{"modelId":"m","inputs":{"prompt":"一只橘猫在跳舞"}}`,
		`{"modelId":"m","inputs":{"prompt":"海边日落","images":["media://a"]}}`,
	} {
		if _, err := repo.Create(Task{UserID: 1, ModelID: "m", Status: "succeeded", Input: json.RawMessage(in)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		search string
		want   int
	}{{"橘猫", 1}, {"日落", 1}, {"狗", 0}, {"prompt", 0}} { // JSON_SEARCH 只搜值不搜键
		_, total, err := repo.List(ListFilter{UserID: 1, Search: tc.search})
		if err != nil {
			t.Fatalf("search=%q: %v", tc.search, err)
		}
		if int(total) != tc.want {
			t.Fatalf("search=%q total=%d want %d", tc.search, total, tc.want)
		}
	}
}

func TestPromoteDueRetriesRespectsLimit(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	for i := 0; i < 3; i++ {
		if _, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, sink, next_retry_at)
			VALUES (1, 'm', 'retry_pending', '{}', '{"kind":"canvas_node"}', datetime('now','-1 minutes'))`); err != nil {
			t.Fatal(err)
		}
	}
	n, err := repo.PromoteDueRetries(2)
	if err != nil || n != 2 {
		t.Fatalf("应只放回 2 个, n=%d err=%v", n, err)
	}
	var left int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE status='retry_pending'`).Scan(&left)
	if left != 1 {
		t.Fatalf("剩余 retry_pending=%d want 1", left)
	}
}

func TestClaimStaleRunningReturnsNewLease(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	res, _ := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, owner_instance, heartbeat_at, lease_version)
		VALUES (1, 'm', 'processing', '{}', 'old', datetime('now','-10 minutes'), 3)`)
	id, _ := res.LastInsertId()
	lease, won, err := repo.ClaimStaleRunning(id, "me", 90)
	if err != nil || !won || lease != 4 {
		t.Fatalf("应接管并拿到代次 4, lease=%d won=%v err=%v", lease, won, err)
	}
	if _, won, _ := repo.ClaimStaleRunning(id, "other", 90); won {
		t.Fatal("刚接管、心跳新鲜的任务不能再被接管")
	}
}

func TestListProcessingStaleFilter(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	ins := func(hb string) int64 {
		res, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, upstream_task_id, owner_instance, heartbeat_at)
			VALUES (1, 'm', 'processing', '{}', 'up', 'x', ` + hb + `)`)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	stale := ins(`datetime('now','-10 minutes')`)
	ins(`CURRENT_TIMESTAMP`)
	got, err := repo.ListProcessing(0, 90)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != stale {
		t.Fatalf("只应取到心跳过期的那条, got %+v", got)
	}
}

func TestUpdateOutputKeepsKindWhenEmpty(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	res, _ := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, output, output_kind)
		VALUES (1, 'm', 'succeeded', '{}', '{}', 'video')`)
	id, _ := res.LastInsertId()
	if err := repo.UpdateOutput(id, json.RawMessage(`{"outputUrls":["media://k"]}`), ""); err != nil {
		t.Fatal(err)
	}
	var kind, typ string
	_ = conn.QueryRow(`SELECT output_kind, typeof(output) FROM generation_tasks WHERE id=?`, id).Scan(&kind, &typ)
	if kind != "video" || typ != "text" {
		t.Fatalf("空 outputKind 不应覆盖原值, kind=%s type=%s", kind, typ)
	}
	if err := repo.UpdateOutput(id, json.RawMessage(`{}`), "image"); err != nil {
		t.Fatal(err)
	}
	_ = conn.QueryRow(`SELECT output_kind FROM generation_tasks WHERE id=?`, id).Scan(&kind)
	if kind != "image" {
		t.Fatalf("非空 outputKind 应覆盖, got %s", kind)
	}
}

func TestOutboxPayloadStoredAsText(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	tx, _ := conn.BeginTx(context.Background(), nil)
	if err := repo.InsertOutboxTx(tx, 5, json.RawMessage(`{"kind":"canvas_node"}`), SinkResult{OutputURLs: []string{"media://k"}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var s, p string
	_ = conn.QueryRow(`SELECT typeof(sink), typeof(payload) FROM generation_sink_outbox WHERE task_id=5`).Scan(&s, &p)
	if s != "text" || p != "text" {
		t.Fatalf("sink=%s payload=%s want text", s, p)
	}
}
