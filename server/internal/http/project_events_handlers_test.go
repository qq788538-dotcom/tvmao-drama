package http

import (
	"bufio"
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/james/huabu_studio/apps/api/internal/events"
)

// 直接挂 handler，绕过 localauth 和 ProjectFromURL，只测 SSE 行为；项目 ID 用 withProjectID 注入。
func eventsServer(t *testing.T, hub *events.Hub, pid int64) *httptest.Server {
	r := chi.NewRouter()
	r.Get("/events", func(w nethttp.ResponseWriter, req *nethttp.Request) {
		ProjectEvents(hub)(w, req.WithContext(withProjectID(req.Context(), pid)))
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestProjectEventsStreamsCLIEvents(t *testing.T) {
	hub := events.NewHub()
	srv := eventsServer(t, hub, 14)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := nethttp.NewRequestWithContext(ctx, "GET", srv.URL+"/events", nil)
	res, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type=%q", ct)
	}
	sc := bufio.NewScanner(res.Body)
	sc.Scan() // 首行注释 ": ok"，确保订阅已建立
	hub.Publish(99, 1, "cli")
	hub.Publish(14, 6, "cli")
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "data: ") {
			if line != `data: {"projectId":14,"version":6,"source":"cli"}` {
				t.Fatalf("line=%q", line)
			}
			return
		}
	}
	t.Fatal("没读到事件")
}

func TestProjectEventsEndsOnHubClose(t *testing.T) {
	hub := events.NewHub()
	srv := eventsServer(t, hub, 14)
	res, err := nethttp.Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	sc.Scan()
	ended := make(chan struct{})
	go func() {
		for sc.Scan() {
		}
		close(ended)
	}()
	hub.Close()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("hub 关闭后 SSE 连接应立即结束")
	}
}
