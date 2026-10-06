package generation

import (
	"fmt"
	"sync"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// §8.2 必跑:lease CAS 的真实并发与旧代次落空——codex 实施审查点名的覆盖缺口。

func TestClaimQueuedConcurrentExactlyOneWins(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	res, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, frozen_cents, sink)
		VALUES (1, 'm-cc', 'queued', '{}', 5, '{"kind":"canvas_node"}')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, _ := res.LastInsertId()

	const workers = 12
	var wg sync.WaitGroup
	wins := make(chan int64, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			lease, won, err := repo.ClaimQueued(id, fmt.Sprintf("inst-%d", n))
			if err != nil {
				errs <- err
				return
			}
			if won {
				wins <- lease
			}
		}(i)
	}
	wg.Wait()
	close(wins)
	close(errs)
	// 断言收紧(codex 复核):任何 DB 错误都算失败——「1 赢 11 错」绝不能算通过。
	for err := range errs {
		t.Fatalf("并发领取出现 DB 错误: %v", err)
	}
	var leases []int64
	for l := range wins {
		leases = append(leases, l)
	}
	if len(leases) != 1 {
		t.Fatalf("并发领取必须恰好一个赢家, got %d 个", len(leases))
	}
	if leases[0] != 1 {
		t.Fatalf("首领代次应为 1, got %d", leases[0])
	}
}

func TestStaleLeaseWritesAllLose(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	res, _ := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, frozen_cents, sink, owner_instance, heartbeat_at, lease_version)
		VALUES (1, 'm-sl', 'running', '{}', 5, '{"kind":"canvas_node"}', 'holder', CURRENT_TIMESTAMP, 4)`)
	id, _ := res.LastInsertId()

	// 旧代次(3)的一切推进写必须落空;有效代次(4)成功。
	if won, err := repo.Heartbeat(id, 3); err != nil || won {
		t.Fatal("旧代次心跳必须落空")
	}
	if won, err := repo.Heartbeat(id, 4); err != nil || !won {
		t.Fatal("有效代次心跳应成功")
	}
	if won, err := repo.MarkUpstreamCalled(id, 3); err != nil || won {
		t.Fatal("旧代次 MarkUpstreamCalled 必须落空")
	}
	if won, err := repo.MarkUpstream(id, 3, "up-x"); err != nil || won {
		t.Fatal("旧代次 MarkUpstream 必须落空")
	}
	if won, err := repo.MarkUpstream(id, 4, "up-x"); err != nil || !won {
		t.Fatal("有效代次 MarkUpstream 应成功")
	}
}

// B1 回归:同秒内重复心跳(值未变,MySQL 报 0 changed rows)绝不能被误判为失租。
func TestHeartbeatSameSecondNotLost(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	res, _ := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, frozen_cents, owner_instance, heartbeat_at, lease_version)
		VALUES (1, 'm-hb', 'running', '{}', 5, 'holder', CURRENT_TIMESTAMP, 2)`)
	id, _ := res.LastInsertId()

	for i := 0; i < 3; i++ {
		won, err := repo.Heartbeat(id, 2)
		if err != nil || !won {
			t.Fatalf("同秒第 %d 次心跳被误判失租 (won=%v err=%v)", i+1, won, err)
		}
	}
}
