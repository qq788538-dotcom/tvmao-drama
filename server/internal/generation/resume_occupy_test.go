package generation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/lifecycle"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

func activeOf(r *ConcurrencyRegistry, modelID string) int {
	r.mu.Lock()
	l := r.byModel[modelID]
	r.mu.Unlock()
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}

func TestOccupyCountsWithoutBlocking(t *testing.T) {
	r := NewConcurrencyRegistry()
	releases := []func(){r.Occupy("m"), r.Occupy("m"), r.Occupy("m")}
	if got := activeOf(r, "m"); got != 3 {
		t.Fatalf("active=%d want 3", got)
	}
	// 上限 2 已被续跑占满（超出也不阻塞）：新任务的 Acquire 必须等。
	acquired := make(chan func())
	go func() { acquired <- r.Acquire("m", 2) }()
	select {
	case <-acquired:
		t.Fatal("名额被续跑占满时 Acquire 不应立即拿到")
	case <-time.After(30 * time.Millisecond):
	}
	releases[0]()
	releases[0]() // 重复归还无副作用
	releases[1]()
	select {
	case rel := <-acquired:
		if got := activeOf(r, "m"); got != 2 {
			t.Fatalf("active=%d want 2", got)
		}
		rel()
	case <-time.After(time.Second):
		t.Fatal("续跑归还后 Acquire 应被唤醒")
	}
	releases[2]()
	if got := activeOf(r, "m"); got != 0 {
		t.Fatalf("全部归还后 active=%d", got)
	}
}

// 孤儿续跑：Occupy 记账，completeAsync 结束时归还。
func TestResumeOrphanedTasksOccupiesModelSlot(t *testing.T) {
	old := resumeStagger
	resumeStagger = 0
	t.Cleanup(func() { resumeStagger = old })

	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"failed","error":"boom"}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-unblock:
		default:
			close(unblock)
		}
	})
	m := &models.ModelConfig{ModelID: "m", Enabled: true, UpstreamBaseURL: srv.URL, UpstreamAPIKey: "sk-a",
		PollingTemplate: json.RawMessage(`{"path":"/t/{{task_id}}","interval":1,"maxWait":60}`)}
	conn, svc := newFingerprintHarness(t, m, okSettings(), "sk-global")
	tracker := &lifecycle.Tracker{}
	svc.taskTracker = tracker
	id := insertOrphan(t, conn, "")

	svc.ResumeOrphanedTasks()
	if got := activeOf(svc.concurrency, "m"); got != 1 {
		t.Fatalf("续跑中应占 1 个名额, active=%d", got)
	}
	close(unblock)
	if !tracker.Wait(10 * time.Second) {
		t.Fatal("completeAsync 未结束")
	}
	if got := activeOf(svc.concurrency, "m"); got != 0 {
		t.Fatalf("completeAsync 结束后应归还名额, active=%d", got)
	}
	if status, _ := taskStatusErr(t, conn, id); status != "failed" {
		t.Fatalf("status=%s", status)
	}
}

func TestResumeDelayCappedBelowLease(t *testing.T) {
	svc := &Service{leaseStaleSec: 90}
	if d := svc.resumeDelay(0); d != 0 {
		t.Fatalf("首个续跑不延迟, got %v", d)
	}
	if d := svc.resumeDelay(2); d != 2*resumeStagger {
		t.Fatalf("got %v", d)
	}
	if d := svc.resumeDelay(100000); d != 30*time.Second {
		t.Fatalf("延迟应封顶在租约超时的 1/3, got %v", d)
	}
}
