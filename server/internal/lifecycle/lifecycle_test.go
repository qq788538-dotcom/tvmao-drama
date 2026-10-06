package lifecycle

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestTracker_GoWaitCompletes(t *testing.T) {
	tr := &Tracker{}
	var ran atomic.Bool
	tr.Go(func() { ran.Store(true) })
	if ok := tr.Wait(time.Second); !ok {
		t.Fatal("快任务应在超时内结束, Wait 应返回 true")
	}
	if !ran.Load() {
		t.Error("goroutine 未执行")
	}
}

func TestTracker_WaitTimesOut(t *testing.T) {
	tr := &Tracker{}
	release := make(chan struct{})
	tr.Go(func() { <-release }) // 卡住直到释放
	if ok := tr.Wait(50 * time.Millisecond); ok {
		t.Fatal("慢任务未结束, Wait 应返回 false(超时)")
	}
	close(release) // 收尾，避免泄漏
}

func TestTracker_Draining(t *testing.T) {
	tr := &Tracker{}
	if tr.Draining() {
		t.Error("初始不应 draining")
	}
	tr.BeginDrain()
	if !tr.Draining() {
		t.Error("BeginDrain 后应 draining")
	}
}
