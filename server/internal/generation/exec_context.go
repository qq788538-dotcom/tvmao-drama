package generation

import (
	"context"
	"errors"
	"time"
)

// 执行链各阶段的 ctx 编排：等名额、调上游/轮询、落盘三段的取消来源各不相同。
//   - 失租：三段都要立刻停（新持有者会走完，旧执行者再写只会 CAS 落空）；
//   - 执行 ctx 的总时限（队列 30 分钟、轮询窗口）：只管调上游/轮询，不管等名额，也不管落盘；
//   - 进程关闭（draining）：只叫停等名额——已经在跑的照常收尾或留给下次启动续跑。

// persistBudget 是一次 finalize 落盘（全部槽位的下载 + 写对象存储，含重试与等下载闸）的
// 时间预算。与执行 ctx 的截止时间脱钩（P22）：轮询窗口末尾出片、孤儿续跑只剩 2 分钟保底窗口时，
// 落盘仍有完整预算，不会因为剩余时间不够转入「保留上游链接」、等上游链接过期后裂图。
// 1 小时够 100KB/s 下完几百 MB；更慢的链路由读空闲超时与手动补搬兜底。包级变量：测试可调短。
var persistBudget = time.Hour

// persistContext 给落盘阶段派生 ctx：context.WithoutCancel(ctx) 去掉执行 ctx 的截止时间与取消，
// 再加 persistBudget 的独立时限；lost（失租信号）结束时一并取消。返回的 cancel 必须调用。
func persistContext(ctx, lost context.Context) (context.Context, context.CancelFunc) {
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), persistBudget)
	if lost == nil {
		return pctx, cancel
	}
	stop := context.AfterFunc(lost, cancel)
	return pctx, func() {
		stop()
		cancel()
	}
}

// extendDeadline 把 ctx 的截止时间顺延 by（排名额花掉的时间），父 ctx 的主动取消照样传下来。
// 父 ctx 没有截止时间或 by<=0 时只是包一层 WithCancel。
func extendDeadline(ctx context.Context, by time.Duration) (context.Context, context.CancelFunc) {
	dl, ok := ctx.Deadline()
	if !ok || by <= 0 {
		return context.WithCancel(ctx)
	}
	ext, cancel := context.WithDeadline(context.WithoutCancel(ctx), dl.Add(by))
	stop := context.AfterFunc(ctx, func() {
		// 父 ctx 到期（DeadlineExceeded）是本函数要顺延的那个时限，不传；主动取消（失租、调用方放弃）要传。
		if errors.Is(ctx.Err(), context.Canceled) {
			cancel()
		}
	})
	return ext, func() {
		stop()
		cancel()
	}
}

// slotDrainPollInterval 是等名额期间检查「进程正在关闭」的间隔。lifecycle 只有 Draining() 布尔量、
// 没有通知通道，只能轮询；壳给关闭留 3 秒（genDrainTimeout 2 秒），间隔必须明显更短。
var slotDrainPollInterval = 200 * time.Millisecond

// slotWaitAbort 返回等名额用的中止 ctx：lost 结束（失租）或进程开始排空时结束。
// 返回的 stop 必须在 acquire 返回后调用，回收后台检查。
func (s *Service) slotWaitAbort(lost context.Context) (context.Context, func()) {
	abort, cancel := context.WithCancel(lost)
	go func() {
		t := time.NewTicker(slotDrainPollInterval)
		defer t.Stop()
		for {
			if s.draining() {
				cancel()
				return
			}
			select {
			case <-abort.Done():
				return
			case <-t.C:
			}
		}
	}()
	return abort, cancel
}
