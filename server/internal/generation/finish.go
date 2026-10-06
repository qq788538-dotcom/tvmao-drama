package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// 终态收口原语(v5 §5.2,保证 2+3)。
//
// 所有出口的唯一入口:状态 CAS(带 lease_version)、结算/退款、插 outbox 三件事在
// **一个事务**里完成——任一步失败整体回滚,「终态写了钱没动」「钱动了终态没写」
// 「终态写了结果没送」三类中间态从结构上消失。
//
// 代码里不再允许裸的 MarkFailed+refund 组合(§8.4 grep 验收);
// settling 中间态与 ClaimSettlement 随之删除——有了原子终态,不需要结算占位。
//
// 失租语义:CAS 影响 0 行 → 回滚返回 won=false,调用方静默退出——不退款不标失败,
// 那次上游调用的成本平台承担、结果丢弃(「设计」§5.4 既定取舍),打点告警。

// finishOutcome 是一次终态收口的全部材料。
type finishOutcome struct {
	success       bool
	output        json.RawMessage // success:任务 output JSON
	outputKind    string          // success:产物类型大类(image/video/audio);空=NULL(纯文本/未知mime)
	actualCredits int64           // success:实结积分
	auditFlag     string          // success:结算审计标记
	reason        string          // failure:失败原因(写 error 列并投递)
	note          string          // 账单交易详情
	sinkRes       SinkResult      // 投递载荷(sink 为空的任务自动跳过)
}

// finishSuccessTx 成功收口:CAS → SettleTx → outbox → commit。
func (s *Service) finishSuccessTx(ctx context.Context, taskID, lease int64, out finishOutcome) (bool, error) {
	out.success = true
	return s.finishTx(ctx, taskID, out,
		`UPDATE generation_tasks SET status='succeeded', output=?, output_kind=NULLIF(?,''), cost_cents=?, audit_flag=?, error=NULL
		 WHERE id=? AND lease_version=? AND status IN ('running','processing','settling')`,
		string(out.output), out.outputKind, out.actualCredits, out.auditFlag, taskID, lease)
}

// finishFailureTx 失败收口:CAS → RefundTx → outbox → commit。
func (s *Service) finishFailureTx(ctx context.Context, taskID, lease int64, reason, note string) (bool, error) {
	out := finishOutcome{reason: reason, note: note, sinkRes: SinkResult{Err: reason}}
	return s.finishTx(ctx, taskID, out,
		`UPDATE generation_tasks SET status='failed', error=?
		 WHERE id=? AND lease_version=? AND status IN ('running','processing','settling')`,
		reason, taskID, lease)
}

// finishQueuedTimeoutTx 排队超时终结:queued 行没有执行者、无 lease 可传,
// CAS 以 status='queued' 为前置并在语句内 lease+1(防与并发领取者竞态)——
// 领取者若恰好同刻抢到,两者恰好一个成功(v5 §5.2/§5.3 矩阵)。
func (s *Service) finishQueuedTimeoutTx(ctx context.Context, taskID int64, reason, note string) (bool, error) {
	out := finishOutcome{reason: reason, note: note, sinkRes: SinkResult{Err: reason}}
	return s.finishTx(ctx, taskID, out,
		`UPDATE generation_tasks SET status='failed', error=?, lease_version=lease_version+1
		 WHERE id=? AND status='queued'`,
		reason, taskID)
}

// finishTx 是三个原语共用的内部实现——CAS、资金、outbox、commit 只写一遍。
func (s *Service) finishTx(ctx context.Context, taskID int64, out finishOutcome, casSQL string, casArgs ...any) (bool, error) {
	if s.db == nil {
		return false, fmt.Errorf("finishTx: db unavailable")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	// ① 状态 CAS:0 行 = 失租/已被推进 → 回滚,won=false。
	res, err := tx.ExecContext(ctx, casSQL, casArgs...)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		log.Printf("[gen-finish] task=%d 终态 CAS 落空(失租或已推进),静默退出", taskID)
		return false, nil
	}

	// 行已被本事务锁定,读资金与投递材料。
	var userID, frozen int64
	var sink []byte
	if err := tx.QueryRowContext(ctx,
		`SELECT user_id, frozen_cents, sink FROM generation_tasks WHERE id=?`,
		taskID).Scan(&userID, &frozen, &sink); err != nil {
		return false, err
	}

	// ② 资金收口:错误让整个事务回滚——绝不能像旧代码那样打个日志继续。
	if out.success {
		if _, err := s.billing.SettleTx(ctx, tx, userID, taskID, frozen, out.actualCredits, out.note); err != nil {
			return false, fmt.Errorf("settle: %w", err)
		}
	} else {
		if _, err := s.billing.RefundTx(ctx, tx, userID, taskID, frozen, out.note); err != nil {
			return false, fmt.Errorf("refund: %w", err)
		}
	}

	// ③ 插 outbox(sink 为空的老任务/直连任务跳过——调用方自取结果)。
	// uk_outbox_task 撞键 = 协议外状态(终态被提交两次):整个事务回滚(安全失败)并告警。
	if len(sink) > 0 {
		out.sinkRes.TaskID = taskID
		if repo, ok := s.repo.(*SQLiteRepository); ok {
			if err := repo.InsertOutboxTx(tx, taskID, sink, out.sinkRes); err != nil {
				log.Printf("[gen-finish] 告警: task=%d 插 outbox 失败(撞键=协议外状态): %v", taskID, err)
				return false, fmt.Errorf("insert outbox: %w", err)
			}
		}
	}

	// ④ commit——终态、钱、投递意图原子生效。
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
