package events

import (
	"testing"
	"time"
)

func recv(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(time.Second):
		t.Fatal("没收到事件")
	}
	return Event{}
}

func TestHubDeliversToSameProjectOnly(t *testing.T) {
	h := NewHub()
	a, cancelA := h.Subscribe(1)
	defer cancelA()
	b, cancelB := h.Subscribe(2)
	defer cancelB()

	h.Publish(1, 5, "cli")
	if e := recv(t, a); e != (Event{ProjectID: 1, Version: 5, Source: "cli"}) {
		t.Fatalf("e=%+v", e)
	}
	select {
	case e := <-b:
		t.Fatalf("项目 2 不该收到: %+v", e)
	default:
	}
}

// 慢订阅者不能阻塞发布方（发布方是 CLI 的写请求）。缓冲满了就丢，前端重连时会比较版本号补上。
func TestHubDropsWhenSubscriberSlow(t *testing.T) {
	h := NewHub()
	_, cancel := h.Subscribe(1)
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := int64(0); i < 1000; i++ {
			h.Publish(1, i, "cli")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish 被慢订阅者阻塞")
	}
}

func TestHubUnsubscribeAndClose(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe(1)
	cancel()
	cancel() // 幂等
	h.Publish(1, 1, "cli")
	if _, ok := <-ch; ok {
		t.Fatal("退订后 channel 应已关闭")
	}

	ch2, _ := h.Subscribe(1)
	h.Close()
	h.Close() // 幂等
	if _, ok := <-ch2; ok {
		t.Fatal("Close 后 channel 应已关闭")
	}
	select {
	case <-h.Done():
	default:
		t.Fatal("Close 后 Done 应可读")
	}
	h.Publish(1, 2, "cli") // Close 后发布不 panic
	ch3, cancel3 := h.Subscribe(1)
	defer cancel3()
	if _, ok := <-ch3; ok {
		t.Fatal("Close 后再订阅应拿到已关闭的 channel")
	}
}
