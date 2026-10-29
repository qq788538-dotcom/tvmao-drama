package generation

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 落盘 ctx 不继承执行 ctx 的截止时间（轮询窗口末尾出片也有完整预算，P22），但失租照样取消。
func TestPersistContextDetachesDeadlineButFollowsLost(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancelParent()
	<-parent.Done()
	lost, markLost := context.WithCancel(context.Background())
	pctx, cancel := persistContext(parent, lost)
	defer cancel()
	if pctx.Err() != nil {
		t.Fatalf("执行 ctx 已过期，落盘 ctx 不应跟着过期: %v", pctx.Err())
	}
	if dl, ok := pctx.Deadline(); !ok || time.Until(dl) < persistBudget-time.Minute {
		t.Fatalf("落盘 ctx 应有完整预算, deadline=%v ok=%v", dl, ok)
	}
	markLost()
	select {
	case <-pctx.Done():
	case <-time.After(time.Second):
		t.Fatal("失租后落盘 ctx 应取消")
	}
}

func TestExtendDeadlineShiftsDeadlineAndKeepsCancel(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), 50*time.Millisecond)
	ext, cancel := extendDeadline(parent, time.Hour)
	defer cancel()
	time.Sleep(100 * time.Millisecond)
	if parent.Err() == nil || ext.Err() != nil {
		t.Fatalf("父 ctx 到期不应传给顺延后的 ctx: parent=%v ext=%v", parent.Err(), ext.Err())
	}
	cancelParent() // 已到期后的 cancel 不改变 Err（仍是 DeadlineExceeded）
	if ext.Err() != nil {
		t.Fatalf("到期不是主动取消: %v", ext.Err())
	}

	p2, cancel2 := context.WithTimeout(context.Background(), time.Hour)
	ext2, c2 := extendDeadline(p2, time.Minute)
	defer c2()
	cancel2()
	select {
	case <-ext2.Done():
		if !errors.Is(ext2.Err(), context.Canceled) {
			t.Fatalf("err=%v", ext2.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("父 ctx 主动取消应传下来")
	}
}

func TestAcquireAbortableGivesUpWithoutTakingSlot(t *testing.T) {
	r := NewConcurrencyRegistry()
	hold := r.Acquire("m", 1)
	abort, stop := context.WithCancel(context.Background())
	got := make(chan error, 1)
	go func() {
		_, err := r.AcquireAbortable("m", 1, abort)
		got <- err
	}()
	// 另一个不可中止的等待者：放弃者退出后，它仍应在名额归还时拿到。
	other := make(chan func(), 1)
	go func() { other <- r.Acquire("m", 1) }()
	time.Sleep(30 * time.Millisecond)
	stop()
	select {
	case err := <-got:
		if !errors.Is(err, errSlotWaitAborted) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("abort 后应放弃等待")
	}
	if a := activeOf(r, "m"); a != 1 {
		t.Fatalf("放弃者不应占名额, active=%d", a)
	}
	hold()
	select {
	case rel := <-other:
		rel()
	case <-time.After(time.Second):
		t.Fatal("名额归还后其余等待者应被唤醒")
	}
	// 有空位时 abort 已结束也照常拿到（只有真要等的时候才放弃）。
	rel, err := r.AcquireAbortable("m", 1, abort)
	if err != nil {
		t.Fatalf("有空位不应失败: %v", err)
	}
	rel()
	rel() // sync.Once：重复归还无副作用
	if a := activeOf(r, "m"); a != 0 {
		t.Fatalf("active=%d", a)
	}
}
