// 本文件是 P5 的契约桩：P1b/P1c 尚未合并时让 agent 包能编译。内容逐字取自参考仓
// fea92ab 的同名声明。最终合并时，只要该包已有真实实现（go build 报重复声明），就删除本文件。

package billing

import "context"

type Result struct {
	NewBalanceCredits int64 `json:"newBalanceCredits"`
}

type actorKey struct{}

func WithActor(ctx context.Context, actorUserID int64) context.Context {
	return context.WithValue(ctx, actorKey{}, actorUserID)
}

func ActorFromContext(ctx context.Context) int64 {
	if v, ok := ctx.Value(actorKey{}).(int64); ok {
		return v
	}
	return 0
}
