package library

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

type fakeDeleter struct {
	mu      sync.Mutex
	deleted []string
	failOn  map[string]bool
}

func (f *fakeDeleter) DeleteObject(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn[key] {
		return errors.New("文件被占用")
	}
	f.deleted = append(f.deleted, key)
	return nil
}

func markOld(t *testing.T, conn *sql.DB, orphans *SQLiteOrphansRepository, key string) {
	t.Helper()
	if err := orphans.Mark(conn, key, 1, 10); err != nil {
		t.Fatal(err)
	}
	conn.Exec(`UPDATE orphan_objects SET marked_at = '2000-01-01 00:00:00' WHERE object_key = ?`, key)
}

func orphanKeys(t *testing.T, conn *sql.DB) []string {
	t.Helper()
	rows, err := conn.Query(`SELECT object_key FROM orphan_objects ORDER BY object_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		rows.Scan(&k)
		out = append(out, k)
	}
	return out
}

func TestSweepDeletesOnlyOldUnreferencedOrphans(t *testing.T) {
	conn := dbtest.Open(t)
	orphans := NewSQLiteOrphansRepository(conn)
	del := &fakeDeleter{failOn: map[string]bool{"locked": true}}
	markOld(t, conn, orphans, "old-free")
	markOld(t, conn, orphans, "old-referenced")
	markOld(t, conn, orphans, "locked")
	orphans.Mark(conn, "young", 1, 10)
	// 宽限期内被重新引用（如撤销删除后又粘回画布）。
	conn.Exec(`INSERT INTO assets (user_id, object_key, url, size_bytes, mime_type) VALUES (1, 'old-referenced', 'media://old-referenced', 0, 'image/png')`)

	sw := NewOrphanSweeper(orphans, del, conn)
	cands, err := sw.Candidates(DefaultGracePeriod)
	if err != nil || len(cands) != 3 {
		t.Fatalf("cands=%+v err=%v", cands, err)
	}
	res, err := sw.Sweep(context.Background(), DefaultGracePeriod)
	if err != nil || res.Deleted != 1 || res.Skipped != 2 || res.TotalCandidates != 3 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !reflect.DeepEqual(del.deleted, []string{"old-free"}) {
		t.Fatalf("deleted=%v", del.deleted)
	}
	// 被重新引用的撤销孤儿标记；删文件失败的保留，下一轮重试；未过宽限期的不动。
	if got := orphanKeys(t, conn); !reflect.DeepEqual(got, []string{"locked", "young"}) {
		t.Fatalf("剩余孤儿=%v", got)
	}
}

// 桌面端没有运维去调 /admin/gc：sidecar 启动时必须立即清扫一次（之后每天一次）。
func TestSweeperStartRunsImmediately(t *testing.T) {
	conn := dbtest.Open(t)
	orphans := NewSQLiteOrphansRepository(conn)
	markOld(t, conn, orphans, "k")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	NewOrphanSweeper(orphans, &fakeDeleter{}, conn).Start(ctx, time.Hour, DefaultGracePeriod)
	deadline := time.Now().Add(3 * time.Second)
	for len(orphanKeys(t, conn)) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("Start 后 3 秒内没有执行第一次清扫")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 关闭时 main 要等 worker 真正退出再关库：Start 返回的通道在 ctx 取消后关闭。
// interval<=0 是配置错误，不能让 time.NewTicker 在 goroutine 里 panic 掉整个 sidecar。
func TestSweeperStartDoneClosesAfterCancelAndToleratesZeroInterval(t *testing.T) {
	conn := dbtest.Open(t)
	orphans := NewSQLiteOrphansRepository(conn)
	ctx, cancel := context.WithCancel(context.Background())
	done := NewOrphanSweeper(orphans, &fakeDeleter{}, conn).Start(ctx, 0, DefaultGracePeriod)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消 3 秒后 sweeper 仍未退出")
	}
}
