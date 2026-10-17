package db_test

import (
	"sort"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

func columns(t *testing.T, table string) []string {
	t.Helper()
	conn := dbtest.Open(t)
	rows, err := conn.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestGenerationTablesCreated(t *testing.T) {
	want := map[string][]string{
		"generation_tasks": {"attempt", "audit_flag", "cost_cents", "created_at", "error", "frozen_cents",
			"heartbeat_at", "id", "input", "lease_version", "model_id", "next_retry_at", "output", "output_kind",
			"owner_instance", "project_id", "sink", "status", "updated_at", "upstream_called_at", "upstream_fingerprint", "upstream_task_id", "user_id"},
		"generation_sink_outbox": {"attempt", "created_at", "delivery_version", "id", "last_error", "locked_until",
			"next_retry_at", "payload", "sink", "state", "task_id", "updated_at"},
	}
	for table, cols := range want {
		got := columns(t, table)
		if len(got) != len(cols) {
			t.Fatalf("%s 列 = %v，want %v", table, got, cols)
		}
		for i := range cols {
			if got[i] != cols[i] {
				t.Fatalf("%s 列 = %v，want %v", table, got, cols)
			}
		}
	}
}

// id 进对象 key（gen/task-<id>-<i>），删除后绝不能被复用，否则新任务会覆盖旧任务的媒体文件。
func TestGenerationTaskIDsNeverReused(t *testing.T) {
	conn := dbtest.Open(t)
	ins := func() int64 {
		res, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, input) VALUES (1, 'm', '{}')`)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	first := ins()
	if _, err := conn.Exec(`DELETE FROM generation_tasks WHERE id = ?`, first); err != nil {
		t.Fatal(err)
	}
	if second := ins(); second <= first {
		t.Fatalf("删除后新 id=%d 复用了旧 id=%d", second, first)
	}
}

func TestGenerationTasksDefaultsAndUpdatedAtTrigger(t *testing.T) {
	conn := dbtest.Open(t)
	res, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, input, updated_at) VALUES (1, 'm', '{}', '2000-01-01 00:00:00')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	var status string
	var lease, attempt int64
	if err := conn.QueryRow(`SELECT status, lease_version, attempt FROM generation_tasks WHERE id = ?`, id).Scan(&status, &lease, &attempt); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || lease != 0 || attempt != 0 {
		t.Fatalf("默认值 status=%s lease=%d attempt=%d", status, lease, attempt)
	}
	if _, err := conn.Exec(`UPDATE generation_tasks SET status = 'running' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	var updated string
	if err := conn.QueryRow(`SELECT updated_at FROM generation_tasks WHERE id = ?`, id).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if updated[:4] == "2000" {
		t.Fatal("UPDATE 后 updated_at 没有刷新")
	}
}

// JSON 列必须是 TEXT：BLOB 会被 SQLite 3.45+ 的 json 函数当作 JSONB。
func TestGenerationJSONColumnsRejectBlob(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, input) VALUES (1, 'm', ?)`, []byte(`{}`)); err == nil {
		t.Fatal("input 以 []byte 写入应被 CHECK 约束拒绝")
	}
	if _, err := conn.Exec(`INSERT INTO generation_sink_outbox (task_id, sink, payload) VALUES (1, ?, '{}')`, []byte(`{}`)); err == nil {
		t.Fatal("sink 以 []byte 写入应被 CHECK 约束拒绝")
	}
}

func TestOutboxOneRowPerTask(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO generation_sink_outbox (task_id, sink, payload) VALUES (7, '{}', '{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO generation_sink_outbox (task_id, sink, payload) VALUES (7, '{}', '{}')`); err == nil {
		t.Fatal("同一任务第二行 outbox 应撞 uk_outbox_task")
	}
}
