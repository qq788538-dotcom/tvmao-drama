package generation

import (
	"context"
	"log"
)

// 项目删除后的在途任务收口（P9）。
//
// 删项目只是软删（projects.deleted_at），后台 ProjectCleaner 慢慢清资产、删终态任务行；
// 在途任务（排队、等重试、执行中、轮询中）以前照常跑完，产物变成删不掉的孤儿资产、配额永久泄漏。
// 这里由生成链路自己发现「项目已删」并收口——不依赖删除入口通知，删除入口改不改都成立：
//   - 一律走带退款的失败收口（finishTx：CAS + RefundTx + outbox 同事务）。**绝不能**直接把行
//     UPDATE 成 failed：在途行持有冻结额度，不经结算路径钱就永远冻着（见 DeleteForProject 注释）。
//   - CAS 里 lease+1：执行中的 goroutine 下一拍心跳即失租、取消执行 ctx，后续写入全部落空，
//     不会再下载落盘，也不会再插资产行。
//   - 心跳间隔内恰好出片的，由 persistSlot 资产事务里的项目检查兜底（persist_asset.go）。

// deletedProjectTaskLister 由 SQLiteRepository 实现；做成可选接口，测试的 fake repo 不必实现。
type deletedProjectTaskLister interface {
	ListActiveInDeletedProjects(limit int) ([]int64, error)
}

// activeTaskStatuses 是项目删除时需要收口的非终态。
const activeTaskStatuses = `('queued','retry_pending','running','processing','settling')`

// ListActiveInDeletedProjects 列出所属项目已软删（或已硬删、行不存在）的非终态任务。
// project_id 为空或 0 的任务不属于任何项目，不在此列。
func (r *SQLiteRepository) ListActiveInDeletedProjects(limit int) ([]int64, error) {
	rows, err := r.db.Query(`SELECT t.id FROM generation_tasks t
		WHERE t.status IN `+activeTaskStatuses+`
		  AND t.project_id IS NOT NULL AND t.project_id <> 0
		  AND NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = t.project_id AND p.deleted_at IS NULL)
		ORDER BY t.id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// finishProjectDeletedTx 项目已删除的失败收口：与 finishQueuedTimeoutTx 同理，收口方不是执行者、
// 手里没有 lease，CAS 以「仍是非终态」为前置并在语句内 lease+1——与执行者的终态 CAS 恰好一个成功。
func (s *Service) finishProjectDeletedTx(ctx context.Context, taskID int64) (bool, error) {
	out := finishOutcome{reason: projectDeletedReason, note: projectDeletedNote, sinkRes: SinkResult{Err: projectDeletedReason}}
	return s.finishTx(ctx, taskID, out,
		`UPDATE generation_tasks SET status='failed', error=?, lease_version=lease_version+1
		 WHERE id=? AND status IN `+activeTaskStatuses,
		projectDeletedReason, taskID)
}

// projectDeletedNote 是项目删除收口的退款备注。
const projectDeletedNote = "项目已删除，已退款"

// SweepDeletedProjectTasks 收口所属项目已删除的在途任务。随 recoverOnce 周期执行。
func (s *Service) SweepDeletedProjectTasks() {
	lister, ok := s.repo.(deletedProjectTaskLister)
	if !ok || s.db == nil {
		return
	}
	ids, err := lister.ListActiveInDeletedProjects(100)
	if err != nil {
		log.Printf("[gen] 扫描已删项目的在途任务失败: %v", err)
		return
	}
	for _, id := range ids {
		if won, err := s.finishProjectDeletedTx(context.Background(), id); err != nil {
			log.Printf("[gen] task=%d 项目已删除收口出错: %v", id, err)
		} else if won {
			log.Printf("[gen] task=%d 所属项目已删除，已取消并退款", id)
		}
	}
}
