package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(Options{Server: srv.URL, Token: "jwt-abc", Version: "9.9.9", RequestID: "req-fixed"}), srv
}

func TestClientSendsAuthUserAgentAndRequestID(t *testing.T) {
	var gotAuth, gotUA, gotReqID, gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotUA, gotReqID, gotPath = r.Header.Get("Authorization"), r.Header.Get("User-Agent"),
			r.Header.Get("X-Request-ID"), r.URL.Path
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	if _, err := c.ListProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer jwt-abc" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotUA != "miaoju-cli/9.9.9" {
		t.Fatalf("User-Agent = %q", gotUA)
	}
	if gotReqID != "req-fixed" {
		t.Fatalf("X-Request-ID = %q", gotReqID)
	}
	// server 只是 origin，前缀由 client 拼——拼错会变成双 /api/v1/cli。
	if gotPath != "/api/v1/cli/projects" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestClientDecodesAPIError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "req-9")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"PROJECT_NOT_FOUND","message":"项目 7 不存在","retryable":false,"requestId":"req-9"}}`))
	})
	_, err := c.GetProject(context.Background(), 7)

	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("err type = %T", err)
	}
	if ae.Code != "PROJECT_NOT_FOUND" || ae.Status != 404 || ae.RequestID != "req-9" {
		t.Fatalf("apiErr = %+v", ae)
	}
}

// nginx 502 之类返回 HTML，不能因为解不出 code 就崩，状态码必须保留。
func TestClientHandlesNonContractErrorBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	})
	_, err := c.GetProject(context.Background(), 7)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 502 {
		t.Fatalf("err = %v", err)
	}
}

func TestClientRetriesGETOn503(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	if _, err := c.ListProjects(context.Background()); err != nil {
		t.Fatalf("应重试成功: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
}

// 这是本层最重要的一条：不带幂等键的写请求**绝不重试**。
// 重试一次「运行节点」就是第二次冻结积分。
func TestClientDoesNotRetryWriteWithoutIdempotencyKey(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := c.RunNode(context.Background(), 1, "n-1", nil, "")
	if err == nil {
		t.Fatal("应报错")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("无幂等键的写请求被重试了 %d 次", got)
	}
}

func TestClientRetriesWriteWithIdempotencyKey(t *testing.T) {
	var calls int32
	var keys []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"taskId":42,"status":"processing"}`))
	})
	run, err := c.RunNode(context.Background(), 1, "n-1", nil, "key-1")
	if err != nil || run.TaskID != 42 {
		t.Fatalf("run = %+v err = %v", run, err)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("calls = %d", calls)
	}
	// 每次重试必须带**同一个** key，否则服务端会当成新请求再冻结一次。
	for i, k := range keys {
		if k != "key-1" {
			t.Fatalf("第 %d 次重试的 key = %q", i, k)
		}
	}
}

// 服务端标了 retryable 的错误可以重试（如 IDEMPOTENCY_IN_PROGRESS）。
func TestClientRetriesServerMarkedRetryable(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 2 {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"IDEMPOTENCY_IN_PROGRESS","message":"处理中","retryable":true}}`))
			return
		}
		_, _ = w.Write([]byte(`{"taskId":1,"status":"processing"}`))
	})
	if _, err := c.RunNode(context.Background(), 1, "n-1", nil, "key-1"); err != nil {
		t.Fatalf("= %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

// 业务错误（400/404/409 非 retryable）不该重试——重试只是浪费时间。
func TestClientDoesNotRetryBusinessErrors(t *testing.T) {
	for _, status := range []int{400, 401, 404, 409} {
		var calls int32
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"NODE_NOT_FOUND","message":"x","retryable":false}}`))
		})
		_, _ = c.ListProjects(context.Background())
		if got := atomic.LoadInt32(&calls); got != 1 {
			t.Errorf("status %d 被重试了 %d 次", status, got)
		}
	}
}

func TestClientTraceNeverLogsTokenOrBody(t *testing.T) {
	var log strings.Builder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	c := New(Options{
		Server: srv.URL, Token: "super-secret-jwt", Version: "1",
		Trace: func(format string, a ...any) { log.WriteString(format) },
	})
	_, _ = c.CreateProject(context.Background(), "机密项目名", "机密描述", "")

	out := log.String()
	for _, leak := range []string{"super-secret-jwt", "Authorization", "机密项目名", "机密描述"} {
		if strings.Contains(out, leak) {
			t.Fatalf("trace 泄漏 %q: %s", leak, out)
		}
	}
}

func TestClientHonorsContextCancel(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()

	start := time.Now()
	_, err := c.ListProjects(ctx)
	if err == nil {
		t.Fatal("取消后应报错")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("取消没有及时生效，耗时 %v", time.Since(start))
	}
	var ne *NetworkError
	if !errors.As(err, &ne) {
		t.Fatalf("取消应映射成 NetworkError, got %T", err)
	}
}

// 取消后不该继续重试——那会把用户的 Ctrl+C 拖成几秒。
func TestClientDoesNotRetryAfterCancel(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	c := New(Options{Server: srv.URL, Token: "t", Version: "1"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = c.ListProjects(ctx)
	if got := atomic.LoadInt32(&calls); got > 1 {
		t.Fatalf("取消后仍重试了 %d 次", got)
	}
}

func TestClientEncodesQueryAndPathEscaping(t *testing.T) {
	var gotPath, gotQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	if _, err := c.ListModels(context.Background(), "video", "Seedance 2.0"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/cli/models" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "type=video") || !strings.Contains(gotQuery, "q=Seedance") {
		t.Fatalf("query = %q", gotQuery)
	}
}

// 节点 ID / 模型名可能含空格与非 ASCII，必须转义进 path 而不是破坏 URL。
func TestClientEscapesPathSegments(t *testing.T) {
	var gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"modelId":"x"}`))
	})
	if _, err := c.GetModel(context.Background(), "Seedance 2.0 VIP"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotPath, " ") {
		t.Fatalf("path 未转义: %q", gotPath)
	}
}

func TestClientIfMatchHeader(t *testing.T) {
	var got string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("If-Match")
		_, _ = w.Write([]byte(`{"id":"n-1"}`))
	})
	if _, err := c.CreateNode(context.Background(), 1, CreateNodeInput{Type: "image-input"}, "", 17); err != nil {
		t.Fatal(err)
	}
	if got != `"snapshot-17"` {
		t.Fatalf("If-Match = %q", got)
	}
	// 不传版本时不应发这个头——那会把普通命令变成乐观锁命令。
	c2, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("If-Match")
		_, _ = w.Write([]byte(`{"id":"n-1"}`))
	})
	if _, err := c2.CreateNode(context.Background(), 1, CreateNodeInput{Type: "image-input"}, "", 0); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("不该发 If-Match, got %q", got)
	}
}
