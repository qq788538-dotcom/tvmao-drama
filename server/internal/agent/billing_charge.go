// apps/api/internal/agent/billing_charge.go
package agent

import (
	"context"
	"log"

	"github.com/cloudwego/eino/schema"

	"github.com/james/huabu_studio/apps/api/internal/billing"
)

// agentNoTask 是 agent LLM 往返写进 ledger 的 task_id：agent 的 planner/summarizer
// 往返并不绑定某个 generation task，所以 task_id 恒为 0（DB 侧落 0/NULL）。
const agentNoTask int64 = 0

// agentSettleTimeout 见 loop.go——settle/refund 用脱离请求的 context（触发结算时请求
// context 可能已被客户端断开 cancel），这里复用同一个上限常量。

// usageToRespMap 把 eino 的 TokenUsage 拼成 billing.SettleAmount 的 token 计费要的
// response map。u 为 nil（上游没回 usage）时返回 nil，让 SettleAmount 报错 →
// 调用方回退冻结额。
func usageToRespMap(u *schema.TokenUsage) map[string]any {
	if u == nil {
		return nil
	}
	return map[string]any{"usage": map[string]any{
		"prompt_tokens":         float64(u.PromptTokens),
		"completion_tokens":     float64(u.CompletionTokens),
		"prompt_tokens_details": map[string]any{"cached_tokens": float64(u.PromptTokenDetails.CachedTokens)},
	}}
}

// chargeSettle 在一次 LLM 往返成功后结算：按 usage 算实际积分，算不出（无 usage）
// 回退到冻结额（不退不补）。用脱离请求的 context 防客户端断开 cancel 掉结算。
// taskID 恒为 agentNoTask——agent 的 LLM 往返不绑 generation task。
// reqCtx 是发起这次往返的请求 ctx（WS 握手带下来的那条）：只从它身上取团队 actor，
// 生命周期一概不继承——结算仍跑在独立的 Background ctx 上。不转挂 actor 的话，
// 团队消费在 freeze 时扣了额度，settle 补记/退差与失败退款全部落空（额度只扣不回）。
func chargeSettle(reqCtx context.Context, b AgentBilling, userID, freezeAmt int64, mb billing.ModelBilling, u *schema.TokenUsage, note string) {
	actual, err := billing.SettleAmount(mb, nil, usageToRespMap(u))
	if err != nil {
		actual = freezeAmt
	}
	// usage 对账日志：结算用的 token 数不落库、流水备注里也没有，出「怎么扣了
	// 这个数」的争议时这行是我们侧唯一的现场，否则只能去上游后台翻记录。
	if u != nil {
		log.Printf("agent billing: settle user=%d note=%q prompt=%d cached=%d output=%d freeze=%d actual=%d",
			userID, note, u.PromptTokens, u.PromptTokenDetails.CachedTokens, u.CompletionTokens, freezeAmt, actual)
	} else {
		log.Printf("agent billing: settle user=%d note=%q usage=nil 回退冻结额 freeze=%d", userID, note, freezeAmt)
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentSettleTimeout)
	ctx = billing.WithActor(ctx, billing.ActorFromContext(reqCtx))
	defer cancel()
	if _, serr := b.Settle(ctx, userID, agentNoTask, freezeAmt, actual, note); serr != nil {
		log.Printf("agent billing: settle failed user=%d note=%q: %v", userID, note, serr)
	}
}

// chargeRefund 在一次 LLM 往返失败后退还冻结额（脱离请求 context，同上）。
// taskID 恒为 agentNoTask。
func chargeRefund(reqCtx context.Context, b AgentBilling, userID, freezeAmt int64, note string) {
	ctx, cancel := context.WithTimeout(context.Background(), agentSettleTimeout)
	ctx = billing.WithActor(ctx, billing.ActorFromContext(reqCtx))
	defer cancel()
	if _, rerr := b.Refund(ctx, userID, agentNoTask, freezeAmt, note); rerr != nil {
		log.Printf("agent billing: refund failed user=%d note=%q: %v", userID, note, rerr)
	}
}
