package generation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

// Delete 单事务化的真实 SQLite 验证：①终态删除的全部效果（行删、资产行删、孤儿登记、配额回收）
// 一次生效；②非终态被拦下时零副作用。

func newDeleteTestService(t *testing.T) (*Service, *sql.DB, *[]string) {
	t.Helper()
	conn := dbtest.Open(t)
	marked := &[]string{}
	svc := &Service{
		repo:      NewSQLiteRepository(conn),
		assets:    sqlTestAssets{db: conn},
		users:     sqlTestUsers{db: conn},
		reclaimer: sqlTestReclaimer{marked: marked},
		db:        conn,
	}
	return svc, conn, marked
}

func seedDeleteFixture(t *testing.T, db *sql.DB, status string) (uid, tid int64, key string) {
	t.Helper()
	uid = 1
	if _, err := db.Exec(`UPDATE users SET storage_used_bytes = 300 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	tres, err := db.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, frozen_cents) VALUES (?, 'm-del', ?, '{}', 0)`, uid, status)
	if err != nil {
		t.Fatal(err)
	}
	tid, _ = tres.LastInsertId()
	key = fmt.Sprintf("gen/del-test-obj-%d", tid)
	if _, err := db.Exec(`INSERT INTO assets (user_id, source_task_id, object_key, source_kind, url, size_bytes, mime_type) VALUES (?, ?, ?, 'generation', 'media://x', 300, 'image/png')`, uid, tid, key); err != nil {
		t.Fatal(err)
	}
	return uid, tid, key
}

func countRows(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeleteTerminalTaskFullEffectsAtomic(t *testing.T) {
	svc, db, marked := newDeleteTestService(t)
	uid, tid, key := seedDeleteFixture(t, db, "succeeded")

	if err := svc.Delete(context.Background(), uid, tid); err != nil {
		t.Fatalf("终态删除应成功: %v", err)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM generation_tasks WHERE id=?`, tid) != 0 {
		t.Fatal("任务行应已删除")
	}
	if countRows(t, db, `SELECT COUNT(*) FROM assets WHERE source_task_id=?`, tid) != 0 {
		t.Fatal("资产行应已删除")
	}
	if len(*marked) != 1 || (*marked)[0] != key {
		t.Fatalf("失去全部引用的对象应登记孤儿, got %v", *marked)
	}
	if used := countRows(t, db, `SELECT storage_used_bytes FROM users WHERE id=?`, uid); used != 0 {
		t.Fatalf("配额应同事务回收到 0, got %d", used)
	}
}

func TestDeleteNonTerminalRejectedNoSideEffects(t *testing.T) {
	svc, db, marked := newDeleteTestService(t)
	uid, tid, _ := seedDeleteFixture(t, db, "running")

	if err := svc.Delete(context.Background(), uid, tid); !errors.Is(err, ErrTaskNotTerminal) {
		t.Fatalf("非终态应返回 ErrTaskNotTerminal, got %v", err)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM generation_tasks WHERE id=?`, tid) != 1 {
		t.Fatal("任务行不该被动过")
	}
	if countRows(t, db, `SELECT COUNT(*) FROM assets WHERE source_task_id=?`, tid) != 1 {
		t.Fatal("资产行不该被动过")
	}
	if len(*marked) != 0 {
		t.Fatalf("不该有孤儿登记, got %v", *marked)
	}
	if used := countRows(t, db, `SELECT storage_used_bytes FROM users WHERE id=?`, uid); used != 300 {
		t.Fatalf("配额不该变, got %d", used)
	}
}
