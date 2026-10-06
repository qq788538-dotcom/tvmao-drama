package agent

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/james/huabu_studio/apps/api/internal/billing"
)

// stubAgentBilling 只记录 Settle 收到的实扣额与两个出口收到的 actor，其余照单全收。
type stubAgentBilling struct {
	settledActual int64
	settleActor   int64
	refundActor   int64
}

func (s *stubAgentBilling) Freeze(_ context.Context, _, _, _ int64, _ string) (billing.Result, error) {
	return billing.Result{}, nil
}
func (s *stubAgentBilling) Settle(ctx context.Context, _, _, _, actualCredits int64, _ string) (billing.Result, error) {
	s.settledActual = actualCredits
	s.settleActor = billing.ActorFromContext(ctx)
	return billing.Result{}, nil
}
func (s *stubAgentBilling) Refund(ctx context.Context, _, _, _ int64, _ string) (billing.Result, error) {
	s.refundActor = billing.ActorFromContext(ctx)
	return billing.Result{}, nil
}

// agent 的 settle/refund 跑在脱离请求的 Background ctx 上——actor 必须从请求 ctx
// 转挂过去，否则团队额度只在 freeze 时扣、结算与退款一概不回冲。
func TestChargeReattachesActorOntoBackgroundCtx(t *testing.T) {
	reqCtx := billing.WithActor(context.Background(), 42)
	mb := billing.ModelBilling{FlatFee: 10}

	b := &stubAgentBilling{}
	chargeSettle(reqCtx, b, 6, 200, mb, nil, "AI 助手 · 思考")
	if b.settleActor != 42 {
		t.Fatalf("settle 收到的 actor = %d, want 42", b.settleActor)
	}

	b2 := &stubAgentBilling{}
	chargeRefund(reqCtx, b2, 6, 200, "AI 助手 · 思考")
	if b2.refundActor != 42 {
		t.Fatalf("refund 收到的 actor = %d, want 42", b2.refundActor)
	}

	// 个人路径（请求 ctx 无 actor）转挂出来必须还是 0，不能凭空造一个。
	b3 := &stubAgentBilling{}
	chargeSettle(context.Background(), b3, 6, 200, mb, nil, "AI 助手 · 思考")
	if b3.settleActor != 0 {
		t.Fatalf("个人路径 actor 应为 0, got %d", b3.settleActor)
	}
}

// captureLog 把标准 log 输出重定向进 buffer，返回恢复函数。
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestChargeSettle_LogsUsageForReconciliation(t *testing.T) {
	buf := captureLog(t)
	mb := billing.ModelBilling{Items: []billing.ChargeItem{
		{Unit: "token", Qty: "tokens.output", Price: billing.Price{Fixed: 1000}},
	}}
	u := &schema.TokenUsage{PromptTokens: 3340, CompletionTokens: 3924}
	u.PromptTokenDetails.CachedTokens = 1536

	chargeSettle(context.Background(), &stubAgentBilling{}, 6, 200, mb, u, "对话 · canvas-gpt-5.5")

	out := buf.String()
	for _, want := range []string{"user=6", "prompt=3340", "cached=1536", "output=3924", "freeze=200", "actual=4", "canvas-gpt-5.5"} {
		if !strings.Contains(out, want) {
			t.Errorf("settle 日志缺 %q，实际输出:\n%s", want, out)
		}
	}
}

func TestChargeSettle_LogsNilUsageFallback(t *testing.T) {
	buf := captureLog(t)
	mb := billing.ModelBilling{Items: []billing.ChargeItem{
		{Unit: "token", Qty: "tokens.output", Price: billing.Price{Fixed: 1000}},
	}}

	chargeSettle(context.Background(), &stubAgentBilling{}, 6, 200, mb, nil, "对话 · canvas-gpt-5.5")

	out := buf.String()
	for _, want := range []string{"usage=nil", "freeze=200"} {
		if !strings.Contains(out, want) {
			t.Errorf("无 usage 回退日志缺 %q，实际输出:\n%s", want, out)
		}
	}
}
