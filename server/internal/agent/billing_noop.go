package agent

import (
	"context"

	"github.com/james/huabu_studio/apps/api/internal/billing"
)

// NoopBilling 是桌面端的计费空实现（spec A1）：Freeze/Settle/Refund 永远成功、
// 不记账、不查余额。loop 的 freeze→settle 流程照常走，只是钱不动。
type NoopBilling struct{}

func (NoopBilling) Freeze(context.Context, int64, int64, int64, string) (billing.Result, error) {
	return billing.Result{}, nil
}

func (NoopBilling) Settle(context.Context, int64, int64, int64, int64, string) (billing.Result, error) {
	return billing.Result{}, nil
}

func (NoopBilling) Refund(context.Context, int64, int64, int64, string) (billing.Result, error) {
	return billing.Result{}, nil
}
