package generation

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/lifecycle"
)

// 启动恢复（去掉多实例租约）。
//
// 多实例部署靠 Redis leader 选举跑一组单例 worker，并且故意不在启动时扫孤儿（蓝绿发布时旧实例
// 还活着）。桌面端单实例单进程，这两个前提都不存在：
//   - leader 组拆成这里直接起的 goroutine；
//   - 启动时库里所有「别的 owner」的在途行都来自已经死掉的上个进程，心跳必须立即判过期，
//     否则要干等 LEASE_STALE_SEC（90 秒）才续跑，用户看到的是重开应用后节点卡住一分半。

// recoverInterval 是孤儿扫描周期。
const recoverInterval = 60 * time.Second

// leaseExpirer 由 SQLiteRepository 实现；做成可选接口是为了测试用 fake repo。
type leaseExpirer interface {
	ExpirePreviousLeases(instance string) (tasks, outbox int64, err error)
}

// ExpirePreviousLeases 把 owner 不是本进程的在途任务心跳清空、把上个进程领走的 outbox 行租约
// 拨到过去。之后 ResumeOrphanedTasks / RecoverStaleQueueTasks 的「心跳为空或超时」条件立即命中，
// claimOutboxDue 也能立即重新领取。只在启动时、后台循环开始之前调用一次。
func (r *SQLiteRepository) ExpirePreviousLeases(instance string) (int64, int64, error) {
	res, err := r.db.Exec(`UPDATE generation_tasks SET heartbeat_at = NULL
		WHERE status IN ('running','processing','settling')
		  AND heartbeat_at IS NOT NULL
		  AND (owner_instance IS NULL OR owner_instance <> ?)`, instance)
	if err != nil {
		return 0, 0, err
	}
	tasks, _ := res.RowsAffected()
	res, err = r.db.Exec(`UPDATE generation_sink_outbox SET locked_until = datetime('now', '-1 seconds')
		WHERE state = 'delivering' AND (locked_until IS NULL OR locked_until >= CURRENT_TIMESTAMP)`)
	if err != nil {
		return tasks, 0, err
	}
	outbox, _ := res.RowsAffected()
	return tasks, outbox, nil
}

// StartBackground 启动生成链路的全部后台工作。调用前必须已 RegisterSink 并完成
// SetTempDir / SetLeaseConfig。首轮恢复同步执行（毫秒级，只查库；续 poll 自己起 goroutine），
// 返回后队列 worker 立即可领取被重排的任务。ctx 取消时三个循环退出；返回的通道在三个循环
// 全部退出后关闭，调用方关库前应等它（outbox 投递会写画布，不等会在关库后拿到 "database is closed"）。
func (s *Service) StartBackground(ctx context.Context) <-chan struct{} {
	if le, ok := s.repo.(leaseExpirer); ok {
		tasks, outbox, err := le.ExpirePreviousLeases(lifecycle.InstanceID())
		if err != nil {
			log.Printf("[gen-startup] 过期上个进程的租约失败: %v", err)
		} else if tasks+outbox > 0 {
			log.Printf("[gen-startup] 接管上个进程遗留: 在途任务 %d 个, 投递中 outbox %d 行", tasks, outbox)
		}
	}
	s.recoverOnce()
	var wg sync.WaitGroup
	for _, loop := range []func(context.Context){s.runRecoverLoop, s.RunQueueWorker, s.RunOutboxWorker} {
		wg.Add(1)
		go func(f func(context.Context)) {
			defer wg.Done()
			f(ctx)
		}(loop)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	return done
}

// recoverOnce 顺序：续 poll → 接管四路分流 → 排队超时兜底。
func (s *Service) recoverOnce() {
	s.ResumeOrphanedTasks()
	s.RecoverStaleQueueTasks()
	s.SweepStuckQueued()
}

func (s *Service) runRecoverLoop(ctx context.Context) {
	t := time.NewTicker(recoverInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if ctx.Err() != nil {
				return
			}
			s.recoverOnce()
		}
	}
}
