package billing

import (
	"context"
	"database/sql"
	"testing"
)

// 以下三个接口逐字对应参考仓的计费窄接口：agent.AgentBilling（agent/backbone.go:197）、
// http 的 chatBilling（chat_handlers.go:53）、canvasscript 的 billingService（service.go:153-158）。
// NoopService 必须同时满足它们，P5/P6 才能直接把它传进去。
type agentBilling interface {
	Freeze(ctx context.Context, userID, taskID, amountCredits int64, note string) (Result, error)
	Settle(ctx context.Context, userID, taskID, frozenCredits, actualCredits int64, note string) (Result, error)
	Refund(ctx context.Context, userID, taskID, frozenCredits int64, note string) (Result, error)
}

type canvasScriptBilling interface {
	FreezeTx(ctx context.Context, tx *sql.Tx, userID, taskID, amountCredits int64, note string) (Result, error)
	Refund(ctx context.Context, userID, taskID, frozenCredits int64, note string) (Result, error)
	Settle(ctx context.Context, userID, taskID, frozenCredits, actualCredits int64, note string) (Result, error)
}

type txBilling interface {
	SettleTx(ctx context.Context, tx *sql.Tx, userID, taskID, frozenCredits, actualCredits int64, note string) (Result, error)
	RefundTx(ctx context.Context, tx *sql.Tx, userID, taskID, frozenCredits int64, note string) (Result, error)
}

var (
	_ agentBilling        = (*NoopService)(nil)
	_ canvasScriptBilling = (*NoopService)(nil)
	_ txBilling           = (*NoopService)(nil)
)

func TestNoopServiceAlwaysSucceedsWithoutDB(t *testing.T) {
	s := NewNoopService()
	ctx := context.Background()
	// 桌面端不计费（spec X3）：任何金额、nil 事务都立即成功，不碰数据库。
	if _, err := s.Freeze(ctx, 1, 2, 999999, "n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FreezeTx(ctx, nil, 1, 2, 999999, "n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settle(ctx, 1, 2, 10, 20, "n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleTx(ctx, nil, 1, 2, 10, 20, "n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refund(ctx, 1, 2, 10, "n"); err != nil {
		t.Fatal(err)
	}
	r, err := s.RefundTx(ctx, nil, 1, 2, 10, "n")
	if err != nil || r.NewBalanceCredits != 0 {
		t.Fatalf("r=%+v err=%v", r, err)
	}
}
