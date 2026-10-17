package generation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

// 单进程桌面端：启动时库里 running/processing 行的心跳都来自已经死掉的上个进程，
// 必须立即视为过期，不能等 90 秒（Review Focus 1）。
func TestExpirePreviousLeasesMakesOldHeartbeatsStale(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	ins := func(status, owner string) int64 {
		res, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, owner_instance, heartbeat_at, lease_version)
			VALUES (1, 'm', ?, '{}', ?, CURRENT_TIMESTAMP, 1)`, status, owner)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	oldRunning := ins("running", "old-proc")
	oldProcessing := ins("processing", "old-proc")
	mine := ins("running", "me")
	done := ins("succeeded", "old-proc")
	if _, err := conn.Exec(`INSERT INTO generation_sink_outbox (task_id, sink, payload, state, locked_until)
		VALUES (1, '{}', '{}', 'delivering', datetime('now','+5 minutes'))`); err != nil {
		t.Fatal(err)
	}

	tasks, outbox, err := repo.ExpirePreviousLeases("me")
	if err != nil || tasks != 2 || outbox != 1 {
		t.Fatalf("tasks=%d outbox=%d err=%v, want 2/1", tasks, outbox, err)
	}
	hbNull := func(id int64) bool {
		var isNull bool
		_ = conn.QueryRow(`SELECT heartbeat_at IS NULL FROM generation_tasks WHERE id=?`, id).Scan(&isNull)
		return isNull
	}
	if !hbNull(oldRunning) || !hbNull(oldProcessing) {
		t.Fatal("上个进程的在途任务心跳应被清空（视为过期）")
	}
	if hbNull(mine) || hbNull(done) {
		t.Fatal("本进程的任务与终态任务不该被动")
	}
	var claimable int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_sink_outbox WHERE state='delivering' AND locked_until < CURRENT_TIMESTAMP`).Scan(&claimable)
	if claimable != 1 {
		t.Fatal("上个进程领走的 outbox 行应立即可被重新领取")
	}
}

type startupSpyRepo struct {
	fakeRecoverRepo
	mu    sync.Mutex
	calls []string
}

func (r *startupSpyRepo) rec(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, s)
}

func (r *startupSpyRepo) ExpirePreviousLeases(string) (int64, int64, error) {
	r.rec("expire")
	return 0, 0, nil
}

func (r *startupSpyRepo) ListProcessing(int, int) ([]Task, error) {
	r.rec("ListProcessing")
	return nil, nil
}

func (r *startupSpyRepo) ListStaleRunning(int) ([]Task, error) {
	r.rec("ListStaleRunning")
	return nil, nil
}

func (r *startupSpyRepo) ListQueuedSinkless(int, int) ([]Task, error) {
	r.rec("ListQueuedSinkless")
	return nil, nil
}

func (r *startupSpyRepo) ListQueuedOlderThan(int, int) ([]Task, error) {
	r.rec("ListQueuedOlderThan")
	return nil, nil
}

func TestStartBackgroundExpiresLeasesThenRecovers(t *testing.T) {
	repo := &startupSpyRepo{}
	svc := &Service{repo: repo, leaseStaleSec: 90}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 只验证同步的首轮；后台 goroutine 拿到已取消的 ctx 立即退出
	svc.StartBackground(ctx)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	want := []string{"expire", "ListProcessing", "ListStaleRunning", "ListQueuedSinkless", "ListQueuedOlderThan"}
	if len(repo.calls) < len(want) {
		t.Fatalf("calls=%v want 前缀 %v", repo.calls, want)
	}
	for i, w := range want {
		if repo.calls[i] != w {
			t.Fatalf("calls=%v want 前缀 %v（必须先过期旧租约，再续 poll，再接管，再兜底）", repo.calls, want)
		}
	}
}

// StartBackground 返回的通道在三个循环都随 ctx 退出后关闭，main 关库前据此等待。
func TestStartBackgroundDoneClosesAfterCancel(t *testing.T) {
	svc := &Service{repo: &startupSpyRepo{}, leaseStaleSec: 90}
	ctx, cancel := context.WithCancel(context.Background())
	done := svc.StartBackground(ctx)
	select {
	case <-done:
		t.Fatal("ctx 未取消时不应关闭")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后三个循环应退出")
	}
}
