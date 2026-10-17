package generation

import (
	"context"
	"encoding/json"
	"testing"
)

// finishTx 三件事（终态 CAS、结算/退款、outbox）同事务的真实 SQLite 验证。

func TestFinishSuccessTxAtomic(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	res, _ := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, lease_version)
		VALUES (?, 'm-f', 'processing', '{}', 40, '{"kind":"canvas_node","projectId":1,"nodeId":"n"}', 2)`, uid)
	id, _ := res.LastInsertId()

	out, _ := json.Marshal(map[string]any{"outputUrls": []string{"media://k"}})
	won, err := svc.finishSuccessTx(context.Background(), id, 2, finishOutcome{
		output: out, actualCredits: 25, auditFlag: "credits_settled", note: "测试结算",
		sinkRes: SinkResult{OutputURLs: []string{"media://k"}},
	})
	if err != nil || !won {
		t.Fatalf("成功收口应赢, won=%v err=%v", won, err)
	}
	var status, outputType string
	var cost int64
	_ = conn.QueryRow(`SELECT status, cost_cents, typeof(output) FROM generation_tasks WHERE id=?`, id).Scan(&status, &cost, &outputType)
	if status != "succeeded" || cost != 25 || outputType != "text" {
		t.Fatalf("终态应 succeeded/25/text, got %s/%d/%s", status, cost, outputType)
	}
	ops := spyOf(svc).ops()
	if len(ops) != 1 || ops[0].Op != "SettleTx" || ops[0].Frozen != 40 || ops[0].Actual != 25 {
		t.Fatalf("应在收口事务内结算一次 (40→25), got %v", ops)
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_sink_outbox WHERE task_id=?`, id).Scan(&n)
	if n != 1 {
		t.Fatalf("成功收口应插 outbox, got %d", n)
	}
}

// 失租者的终态 CAS 落空 → 整个事务回滚:状态、计费、outbox 全部原样。
func TestFinishTxStaleLeaseRollsBackEverything(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	res, _ := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, lease_version)
		VALUES (?, 'm-f', 'processing', '{}', 40, '{"kind":"canvas_node"}', 2)`, uid)
	id, _ := res.LastInsertId()

	out, _ := json.Marshal(map[string]any{"outputUrls": []string{"media://k"}})
	won, err := svc.finishSuccessTx(context.Background(), id, 1, finishOutcome{ // 旧代次
		output: out, actualCredits: 25, note: "旧代次结算",
		sinkRes: SinkResult{OutputURLs: []string{"media://k"}},
	})
	if err != nil || won {
		t.Fatalf("旧代次必须落空且无错, won=%v err=%v", won, err)
	}
	status, lease := taskState(t, conn, id)
	if status != "processing" || lease != 2 {
		t.Fatalf("状态必须原样, got %s/%d", status, lease)
	}
	assertOps(t, svc)
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_sink_outbox WHERE task_id=?`, id).Scan(&n)
	if n != 0 {
		t.Fatalf("outbox 必须未插, got %d", n)
	}
}

// 终态只提交一次:已 failed 的任务第二次收口 CAS 直接落空。
func TestFinishTxDoubleTerminalIsSafeFailure(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	res, _ := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, lease_version)
		VALUES (?, 'm-f', 'processing', '{}', 40, '{"kind":"canvas_node"}', 2)`, uid)
	id, _ := res.LastInsertId()
	if won, _ := svc.finishFailureTx(context.Background(), id, 2, "第一次失败", "退款"); !won {
		t.Fatal("第一次收口应赢")
	}
	if won, _ := svc.finishFailureTx(context.Background(), id, 3, "第二次失败", "退款"); won {
		t.Fatal("终态任务绝不能被二次收口")
	}
	assertOps(t, svc, "RefundTx")
}
