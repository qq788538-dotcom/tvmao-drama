package generation

import (
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/lifecycle"
)

// 关闭排空后不再领取新任务；Drain 等在途 goroutine，且有上限。
func TestDrainStopsQueuePickup(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	svc.taskTracker = &lifecycle.Tracker{}
	res, err := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, lease_version)
		VALUES (?, 'm-drain', 'queued', '{}', 0, '{"kind":"canvas_node"}', 0)`, uid)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if !svc.Drain(time.Second) {
		t.Fatal("没有在途任务时 Drain 应立即返回 true")
	}
	svc.pumpQueueOnce()
	if status, _ := taskState(t, conn, id); status != "queued" {
		t.Fatalf("排空后不应再领取任务, got %s", status)
	}
}

func TestDrainWaitIsBounded(t *testing.T) {
	svc := &Service{taskTracker: &lifecycle.Tracker{}}
	release := make(chan struct{})
	defer close(release)
	svc.tracker().Go(func() { <-release })
	start := time.Now()
	if svc.Drain(50 * time.Millisecond) {
		t.Fatal("在途任务未结束, Drain 应超时返回 false")
	}
	if time.Since(start) > time.Second {
		t.Fatal("Drain 必须有上限")
	}
}
