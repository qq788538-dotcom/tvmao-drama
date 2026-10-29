package generation

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/lifecycle"
)

// 队列的执行端与接管。
//
// 执行端：worker 领到任务后从 DB 快照重建执行材料再调上游——因为领到的任务可能是
// 别的实例（甚至上个版本的进程）提交的，内存里没有任何上下文。
//
// 接管：租约过期的 running 任务按「有没有调过上游」分流。这一刀是"部署不再丢任务"
// 与"绝不重复扣费"能同时成立的关键。

// queueRefundNote 是队列路径的退款备注。
// 另一分支(5241dbf)把中断退款改成复用任务原本的消费备注，两边合并后这里应统一过去；
// 现在不直接引用那边的函数，避免跨分支耦合。
const queueRefundNote = "任务被中断，已退款"

// RunQueueWorker 每实例常驻：周期领取并执行排队任务。
// 不是 leader 单例——多实例并行领取靠 ClaimQueued 的 CAS 去重，
// 这样发布期间新实例立刻能干活，不用等旧实例让出队列。
func (s *Service) RunQueueWorker(ctx context.Context) {
	t := time.NewTicker(queuePollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if ctx.Err() != nil {
				return
			}
			s.pumpQueueOnce()
		}
	}
}

// pumpQueueOnce 领取一轮。领到就丢后台执行，本函数不阻塞在生成上——
// 否则一个慢任务会拖住整轮领取。
func (s *Service) pumpQueueOnce() {
	if s.draining() {
		return // 正在关闭：不再领取
	}
	// 到点的 retry_pending 先放回 queued（v5 §4.2：并入同一次 pump，不新增 worker），
	// 本轮领取立刻能把它们抢走。
	if n, err := s.repo.PromoteDueRetries(200); err != nil {
		log.Printf("[gen-queue] 重试到点重排失败: %v", err)
	} else if n > 0 {
		log.Printf("[gen-queue] %d 个重试任务到点，已放回队列", n)
	}

	models, err := s.repo.QueuedModels()
	if err != nil {
		log.Printf("[gen-queue] 取排队模型清单失败: %v", err)
		return
	}
	if len(models) == 0 {
		return
	}
	var pickupRepo queuePickupRepo = s.repo
	if s.concurrency != nil {
		pickupRepo = persistAwarePickupRepo{queuePickupRepo: s.repo, persisting: s.concurrency.PersistingByModel}
	}
	candidates := pickupCandidates(pickupRepo, models, s.modelConcurrencyLimits(models), runningFreshSec)
	for _, task := range candidates {
		if s.draining() {
			return
		}
		lease, won, err := s.repo.ClaimQueued(task.ID, lifecycle.InstanceID())
		if err != nil {
			log.Printf("[gen-queue] 领取 task=%d 失败: %v", task.ID, err)
			continue
		}
		if !won {
			continue // 别的实例先领走了。绝不能继续执行，否则上游会被调两次
		}
		t := task
		l := lease
		s.tracker().Go(func() { s.runQueuedTask(t, l) })
	}
}

// runQueuedTask 执行一个已领取的任务：重建材料 → 调上游 → 原子收口 → outbox 投递。
// lease 由领取 CAS 取得,全程向下传递;心跳与失租取消在 callAndComplete 内统一处理,
// 准备阶段(签名/下载参考图可能超 90s)也在其心跳覆盖之外——所以这里先起一段心跳。
func (s *Service) runQueuedTask(task Task, lease int64) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[gen-queue] panic task=%d: %v", task.ID, rec)
			s.failQueuedTask(task, lease, "内部错误，已退款，请重试")
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	// 领取成功就立刻续租，绝不能等到 prepareExecution 之后——准备阶段可能超过租约期,
	// 期间被接管的话本 goroutine 的后续写全部带旧代次落空,不会双执行,但要尽早止损。
	stopLease := s.startHeartbeat(task.ID, lease, cancel)
	defer stopLease()

	var snap taskSnapshot
	if err := json.Unmarshal(task.Input, &snap); err != nil {
		s.failQueuedTask(task, lease, "任务快照损坏，已退款，请重试")
		return
	}

	plan, err := s.prepareExecution(ctx, task.UserID, snap.ModelID, snap.Inputs, snap.Note, snap.PresetKey, snap.PresetParams)
	if err != nil {
		// 准备阶段失败（模型被停用、key 没配、参考图取不到…）——还没碰上游，
		// 直接失败退款即可，不存在重复扣费的风险。
		log.Printf("[gen-queue] task=%d 准备执行失败: %v", task.ID, err)
		s.failQueuedTask(task, lease, err.Error())
		return
	}

	if _, err := s.callAndComplete(ctx, task.ID, lease, plan.model, plan.apiKey, plan.baseDomain, plan.payload,
		task.UserID, task.ProjectID, task.FrozenCents, plan.normalizedInputs, snap.Inputs, plan.preparedImages, plan.note); err != nil {
		log.Printf("[gen-queue] task=%d 执行失败: %v", task.ID, err)
	}
}

// failQueuedTask 原子失败收口(CAS+退款+outbox 同事务)。失租时 CAS 落空,静默不动钱。
func (s *Service) failQueuedTask(task Task, lease int64, reason string) {
	if _, err := s.finishFailureTx(context.Background(), task.ID, lease, reason, queueRefundNote); err != nil {
		log.Printf("[gen-queue] task=%d 失败收口出错: %v", task.ID, err)
	}
}

// RecoverStaleQueueTasks 是统一接管器（v5 §3.2）：leader 周期接管心跳超时的
// running 任务，**不按 sink 过滤**，按状态表四路分流：
//   - 有 upstream_task_id：交给 ResumeOrphanedTasks 续 poll，这里不插手
//   - 已调上游但没拿到 id：结论不确定 → 保守退款，绝不重排（重排会重复扣费）
//   - 没调过 + 有 sink（队列任务）：零外部副作用 → 放回队列重排，用户无感
//   - 没调过 + 无 sink（直连任务）：无投递通道，接管者无法代为完成同步语义
//     → 失败退款（自轮询的调用方按终态各自处理）
func (s *Service) RecoverStaleQueueTasks() {
	if s.draining() {
		return
	}
	tasks, err := s.repo.ListStaleRunning(s.staleSec())
	if err != nil {
		log.Printf("[gen-queue] 扫描过期 running 失败: %v", err)
		return
	}
	requeued, refunded := 0, 0
	for _, t := range tasks {
		if t.UpstreamTaskID != "" {
			continue // 有上游 id：ResumeOrphanedTasks 会接管续 poll，别在这里插手
		}
		// 一切处置先 CAS 接管取新代次(v5 §5.3)：抢不到 = 心跳其实还活着/别人先接了。
		lease, won, err := s.repo.ClaimStaleRunning(t.ID, lifecycle.InstanceID(), s.staleSec())
		if err != nil || !won {
			continue
		}
		if t.UpstreamCalledAt == nil {
			if len(t.Sink) > 0 {
				// 队列任务、还没碰上游 → 重排,用户无感。RequeueStale 带代次+再推进代次,
				// 失租的旧执行者从此写不进任何东西。
				ok, err := s.repo.RequeueStale(t.ID, lease)
				if err != nil {
					log.Printf("[gen-queue] task=%d 重排失败: %v", t.ID, err)
					continue
				}
				if ok {
					requeued++
				}
				continue
			}
			// 直连孤儿：无投递通道,接管者无法代为完成同步语义 → 原子失败退款。
			if ok, _ := s.finishFailureTx(context.Background(), t.ID, lease, "任务执行中被中断，已退款，请重试", queueRefundNote); ok {
				refunded++
			}
			continue
		}
		// 调过上游但没拿到 id：上游可能已经开始跑了，重排会重复扣费。
		// 宁可平台承担这一次成本，也不让用户被扣两次。原子失败退款(含 outbox 通知)。
		if ok, _ := s.finishFailureTx(context.Background(), t.ID, lease, "任务执行中被中断，结果无法确认，已退款，请重试", queueRefundNote); ok {
			refunded++
		}
	}
	if requeued > 0 || refunded > 0 {
		log.Printf("[gen-queue] 接管完成: 重排 %d 个, 退款 %d 个", requeued, refunded)
	}
}

// SweepStuckQueued 兜底：排队太久仍没被领取的任务判失败退款。
//
// 没有这道兜底，模型被删/上限配成 0/队列永远排不上的任务会永久卡在 queued，
// 冻结的钱再也回不来。
func (s *Service) SweepStuckQueued() {
	// 先兜住"根本没进队列"的那批：它们连下面这条扫描都够不着。
	s.sweepSinklessQueued()

	tasks, err := s.repo.ListQueuedOlderThan(s.queueStuckSec(), 100)
	if err != nil {
		log.Printf("[gen-queue] 扫描滞留 queued 失败: %v", err)
		return
	}
	for _, t := range tasks {
		// queued 行没有执行者、无 lease 可传——finishQueuedTimeoutTx 在 CAS 内 lease+1,
		// 与并发领取者恰好一个成功(v5 §5.2)。
		if won, err := s.finishQueuedTimeoutTx(context.Background(), t.ID, "排队超时未能开始，已退款，请重试", queueRefundNote); err != nil {
			log.Printf("[gen-queue] task=%d 排队超时收口出错: %v", t.ID, err)
		} else if won {
			log.Printf("[gen-queue] task=%d 排队超时，已退款", t.ID)
		}
	}
}

// sweepSinklessQueued 兜住「本该进队列却没有 sink」的任务。
//
// 起因是个真实事故：Task 结构加了 Sink 字段，但 repository.Create 的 INSERT 漏了
// 这一列，于是任务落库时 sink=NULL。队列只领 sink IS NOT NULL 的行，这些任务
// 永远排不上；而 SweepStuckQueued 同样要求 sink 非空，连兜底都够不着它们——
// 冻结的钱会永久卡住，正是这次改造要消灭的那种问题。
//
// 单测没抓到，因为 generation 的测试用 fake repo，从不验证真实 INSERT 的列。
// 所以在运行时加这道兜底：任何 queued 且无 sink、又超过阈值的任务一律退款失败，
// 保证「钱不会永久冻结」这条不变量不依赖于某一处 SQL 写对。
func (s *Service) sweepSinklessQueued() {
	tasks, err := s.repo.ListQueuedSinkless(s.queueStuckSec(), 100)
	if err != nil {
		log.Printf("[gen-queue] 扫描无 sink 的 queued 失败: %v", err)
		return
	}
	for _, t := range tasks {
		if won, err := s.finishQueuedTimeoutTx(context.Background(), t.ID, "任务未能进入队列，已退款，请重试", queueRefundNote); err != nil {
			log.Printf("[gen-queue] task=%d 无 sink 滞留收口出错: %v", t.ID, err)
		} else if won {
			log.Printf("[gen-queue] task=%d 无 sink 滞留，已退款", t.ID)
		}
	}
}
