package generation

import (
	"sync/atomic"
	"testing"
	"time"
)

// 失租语义(§5.3):心跳 won=false 必须触发 onLost(cancel 执行 ctx)——
// codex 实施审查指出此前自查宣称有覆盖但实际没有,补上。

func TestStartHeartbeatLostTriggersOnLost(t *testing.T) {
	repo := &heartbeatSpyRepo{won: false}
	s := &Service{repo: repo, leaseHeartbeatSec: 1} // 1s 心跳,测试很快触发
	var lost atomic.Bool
	stop := s.startHeartbeat(9, 7, func() { lost.Store(true) })
	defer stop()

	deadline := time.Now().Add(5 * time.Second)
	for !lost.Load() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !lost.Load() {
		t.Fatal("心跳 won=false 必须触发 onLost(取消执行)")
	}
}
