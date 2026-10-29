package generation

import (
	"log"
	"time"
)

// 队列 worker：把「排队等待」从进程内存搬进 DB 之后的执行端。
//
// 每个实例都跑（不是 leader 单例）——多实例同时领取靠 ClaimQueued 的 CAS 保证不重复。
// 这样发布期间新实例立刻就能开始干活，而不用等旧实例把队列让出来。

// 队列相关的任务状态。
//
// 「worker 绝不领到没扣钱的任务」现在由事务原子性保证(v5 §5.4):建行与冻结同事务,
// 行只在 Commit 时对外可见,queued 出生即已扣钱——pending_freeze 中间态随之删除
// (存量行在生产上线后人工清点,v5 §6 发布约束第 2 条)。
const (
	statusQueued  = "queued"
	statusRunning = "running"
)

// isPickable 判断某状态的任务能否被队列领取。只有 queued——
// running 已被别人拿走,retry_pending 未到点,终态则早已结束。
func isPickable(status string) bool { return status == statusQueued }

const (
	queuePollInterval = 2 * time.Second
	// defaultModelConcurrency：模型没配 gen_control.concurrency 时的兜底上限。
	// 绝不能取 0——那等于该模型永远排不上，任务最后被兜底超时退款，
	// 用户看到的是"提交了就没反应"。
	defaultModelConcurrency = 200
	// 领取时判定"在跑"的心跳新鲜度。比租约超时略短，避免把刚死的实例的任务算进在跑数
	// 而白白占着名额。
	runningFreshSec = 60
)

// queuePickupRepo 是 pickupCandidates 需要的最小依赖，便于测试注入。
type queuePickupRepo interface {
	RunningCountByModel(freshSec int) (map[string]int, error)
	ListQueuedForModel(modelID string, limit int) ([]Task, error)
}

// pickupCandidates 算出本轮可以领取的任务。
//
// 关键：**按模型分组**领取，而不是按 id 顺序扫全队列。后者会产生队头阻塞——
// 遇到一个满员模型的任务就停下，后面所有其他模型的任务全被堵住，
// 慢模型（视频，几分钟一个）会把快模型（生图，几秒一个）饿死。
//
// limits 是每模型的并发上限（来自各自的 gen_control.concurrency）。
// 模型之间互不影响：这是"并发按模型独立"这条要求的落地点。
// models 必须来自「当前有排队任务的模型清单」(repo.QueuedModels)，不能从 limits 或
// running 反推——一个全新模型既没配上限也还没在跑，那样推不出来，它的任务会被无声饿死
// 直到兜底超时退款。
func pickupCandidates(repo queuePickupRepo, models []string, limits map[string]int, freshSec int) []Task {
	running, err := repo.RunningCountByModel(freshSec)
	if err != nil {
		log.Printf("[gen-queue] 统计在跑数失败: %v", err)
		return nil
	}

	var out []Task
	for _, model := range models {
		limit, ok := limits[model]
		if !ok || limit <= 0 {
			limit = defaultModelConcurrency
		}
		slots := limit - running[model]
		if slots <= 0 {
			continue // 该模型已满：跳过它，但绝不因此停止处理其他模型
		}
		tasks, err := repo.ListQueuedForModel(model, slots)
		if err != nil {
			log.Printf("[gen-queue] 取 %s 排队任务失败: %v", model, err)
			continue
		}
		out = append(out, tasks...)
	}
	return out
}

// persistAwarePickupRepo 把本进程落盘中的任务从在跑数里扣掉（P20）：它们的生成名额已还，
// 行却仍是 running/processing，照数的话下载一慢队列就领不动——名额提前归还对队列任务形同虚设。
// 先查 DB 再取快照：两步之间开始落盘的任务两边都算到，结束落盘的只有 DB 一边算到，
// 都只会少领、不会多领。万一多扣（行已被收口而落盘尚未返回），进程内名额闸照样限住上游在途数。
type persistAwarePickupRepo struct {
	queuePickupRepo
	persisting func() map[string]int
}

func (r persistAwarePickupRepo) RunningCountByModel(freshSec int) (map[string]int, error) {
	running, err := r.queuePickupRepo.RunningCountByModel(freshSec)
	if err != nil || r.persisting == nil {
		return running, err
	}
	for model, n := range r.persisting() {
		if running[model] -= n; running[model] <= 0 {
			delete(running, model)
		}
	}
	return running, nil
}

// QueuedModelsProvider 由 repo 实现：给出当前有排队任务的模型清单。
// 只对这些模型算空位，避免为全量模型做无谓查询。
type QueuedModelsProvider interface {
	QueuedModels() ([]string, error)
}

// modelConcurrencyLimits 读出给定模型各自的并发上限（每模型独立，取自各自的
// gen_control.concurrency）。查不到的模型用默认值，绝不当成 0——
// 那等于让它永远排不上，任务最后被兜底超时退款，用户看到的是"提交了没反应"。
func (s *Service) modelConcurrencyLimits(modelIDs []string) map[string]int {
	out := make(map[string]int, len(modelIDs))
	for _, id := range modelIDs {
		limit := defaultModelConcurrency
		if s.models != nil {
			if m, err := s.models.GetByModelID(id); err == nil && m != nil {
				limit = m.GenConcurrency()
			}
		}
		if limit <= 0 {
			limit = defaultModelConcurrency
		}
		out[id] = limit
	}
	return out
}
