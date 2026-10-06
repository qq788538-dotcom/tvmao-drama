package billing

import (
	"context"
	"database/sql"
	"errors"
)

// 桌面端不做积分计费（spec X3）。这里保留参考仓 billing/service.go:12-20 的错误值与
// Result 类型，让搬过来的调用方原样编译；真正的 Freeze/Settle/Refund 全部换成 NoopService。

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrTaskNotFound        = errors.New("task not found")
)

// Result 与参考仓 billing.Result 同形。
type Result struct {
	NewBalanceCredits int64 `json:"newBalanceCredits"`
}

// NoopService 满足参考仓各处计费窄接口（agent.AgentBilling、canvasscript 的 billingService、
// chat 的 chatBilling；generation.Billing 经 cmd/sidecar 的适配器）。所有操作立即成功、
// 不落库，nil 事务也可以。
type NoopService struct{}

func NewNoopService() *NoopService { return &NoopService{} }

func (*NoopService) Freeze(context.Context, int64, int64, int64, string) (Result, error) {
	return Result{}, nil
}

func (*NoopService) Settle(context.Context, int64, int64, int64, int64, string) (Result, error) {
	return Result{}, nil
}

func (*NoopService) Refund(context.Context, int64, int64, int64, string) (Result, error) {
	return Result{}, nil
}

func (*NoopService) FreezeTx(context.Context, *sql.Tx, int64, int64, int64, string) (Result, error) {
	return Result{}, nil
}

func (*NoopService) SettleTx(context.Context, *sql.Tx, int64, int64, int64, int64, string) (Result, error) {
	return Result{}, nil
}

func (*NoopService) RefundTx(context.Context, *sql.Tx, int64, int64, int64, string) (Result, error) {
	return Result{}, nil
}
