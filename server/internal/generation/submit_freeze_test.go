package generation

import (
	"testing"
)

// 钱的不变量 2(v5 §5.4 后):「worker 绝不领到没扣钱的任务」由事务原子性保证——
// 建行与 FreezeTx 同一个事务,行只在 Commit 时对外可见,queued 出生即已扣钱。
// 状态机层面剩下的守卫:队列只领 queued。

func TestOnlyQueuedIsPickable(t *testing.T) {
	for _, st := range []string{"running", "succeeded", "failed", "retry_pending", "settling"} {
		if isPickable(st) {
			t.Fatalf("状态 %q 不该被队列领取", st)
		}
	}
	if !isPickable(statusQueued) {
		t.Fatal("queued 必须可被领取")
	}
}
