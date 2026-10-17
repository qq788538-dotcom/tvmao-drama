package generation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

// 直连任务(ForceAsync/agent 动态工具)建行即 running,心跳只在 callAndComplete 里起。
// 若心跳排在并发闸 Acquire 之后,同模型名额被占满(视频模型整段轮询都持有名额)超过
// staleSec,接管器就会把这个「还在排名额、什么都没做」的任务当直连孤儿判失败退款,
// 等名额拿到后 MarkUpstreamCalled 又失租——用户凭空收到一次「任务执行中被中断」。
func TestCallAndCompleteHeartbeatsWhileWaitingForConcurrencySlot(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	svc.leaseHeartbeatSec = 1
	svc.leaseStaleSec = 2
	svc.concurrency = NewConcurrencyRegistry()

	res, err := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, owner_instance, heartbeat_at, lease_version)
		VALUES (?, 'm-slot', 'running', '{}', 40, 'me', CURRENT_TIMESTAMP, 1)`, uid)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, _ := res.LastInsertId()

	model := &models.ModelConfig{ModelID: "m-slot", GenControlRaw: json.RawMessage(`{"concurrency":1}`)}
	hold := svc.concurrency.Acquire(model.ModelID, model.GenConcurrency()) // 名额被别的生成占着

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = svc.callAndComplete(ctx, id, 1, model, "k", "http://127.0.0.1:1", []byte(`{}`), uid, nil, 40, nil, nil, "")
	}()

	time.Sleep(time.Duration(svc.leaseStaleSec)*time.Second + 1500*time.Millisecond)
	svc.RecoverStaleQueueTasks()
	status, lease := taskState(t, conn, id)

	// 收尾:取消执行 ctx 再放名额,让上游调用立刻失败、goroutine 退出后再关库。
	cancel()
	hold()
	<-done

	if status != "running" || lease != 1 {
		t.Fatalf("排名额期间心跳必须续上,不能被接管: status=%s lease=%d", status, lease)
	}
}
