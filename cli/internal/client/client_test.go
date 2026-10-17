package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/james/huabu_studio/apps/cli/internal/endpoint"
)

const testCLIToken = "cli-test-token"

// pingWrap 让测试服务器响应 /api/v1/cli/ping，其余请求交给 h。
func pingWrap(app string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/cli/ping" {
			if r.Header.Get("Authorization") != "Bearer "+testCLIToken {
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"app": app, "version": "1.0.0"})
			return
		}
		h.ServeHTTP(w, r)
	})
}

func infoFor(t *testing.T, srv *httptest.Server, token, appVersion string) endpoint.Info {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	return endpoint.Info{Version: 1, Port: port, Token: token, AppVersion: appVersion}
}

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(pingWrap("miaoju-desktop", h))
	t.Cleanup(srv.Close)
	info := infoFor(t, srv, testCLIToken, "9.9.9")
	return New(Options{Resolve: func() (endpoint.Info, error) { return info, nil }, Version: "9.9.9", RequestID: "req-fixed"}), srv
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
	if gotAuth != "Bearer "+testCLIToken {
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
// 重试一次「运行节点」就是第二次跑上游。
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
	// 每次重试必须带**同一个** key，否则服务端会当成新请求再提交一次。
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
		want := int32(1)
		if status == 401 {
			want = 2 // 401 不退避重试，但会重读地址文件后再发一次
		}
		if got := atomic.LoadInt32(&calls); got != want {
			t.Errorf("status %d 被请求了 %d 次，应为 %d", status, got, want)
		}
	}
}

func TestClientTraceNeverLogsTokenOrBody(t *testing.T) {
	var log strings.Builder
	srv := httptest.NewServer(pingWrap("miaoju-desktop", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	})))
	t.Cleanup(srv.Close)

	info := infoFor(t, srv, testCLIToken, "")
	c := New(Options{
		Resolve: func() (endpoint.Info, error) { return info, nil }, Version: "1",
		Trace: func(format string, a ...any) { log.WriteString(format) },
	})
	_, _ = c.CreateProject(context.Background(), "机密项目名", "机密描述", "")

	out := log.String()
	for _, leak := range []string{testCLIToken, "Authorization", "机密项目名", "机密描述"} {
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
	srv := httptest.NewServer(pingWrap("miaoju-desktop", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})))
	t.Cleanup(srv.Close)
	info := infoFor(t, srv, testCLIToken, "")
	c := New(Options{Resolve: func() (endpoint.Info, error) { return info, nil }, Version: "1"})

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

func TestMissingEndpointIsAppNotRunning(t *testing.T) {
	c := New(Options{Resolve: func() (endpoint.Info, error) { return endpoint.Info{}, endpoint.ErrNotRunning }})
	_, err := c.ListProjects(context.Background())
	var anr *AppNotRunningError
	if !errors.As(err, &anr) {
		t.Fatalf("err=%T %v", err, err)
	}
}

func TestConnRefusedIsAppNotRunningWithoutRetry(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	info := infoFor(t, srv, testCLIToken, "")
	srv.Close() // 端口已无人监听
	c := New(Options{Resolve: func() (endpoint.Info, error) { return info, nil }})
	start := time.Now()
	_, err := c.ListProjects(context.Background())
	var anr *AppNotRunningError
	if !errors.As(err, &anr) {
		t.Fatalf("err=%T %v", err, err)
	}
	if time.Since(start) > 250*time.Millisecond {
		t.Fatal("连接被拒不应退避重试")
	}
}

func TestPingRejectsForeignApp(t *testing.T) {
	srv := httptest.NewServer(pingWrap("something-else", http.NotFoundHandler()))
	defer srv.Close()
	info := infoFor(t, srv, testCLIToken, "")
	c := New(Options{Resolve: func() (endpoint.Info, error) { return info, nil }})
	_, err := c.ListProjects(context.Background())
	var anr *AppNotRunningError
	if !errors.As(err, &anr) {
		t.Fatalf("端口上不是喵剧应视为未运行，err=%T %v", err, err)
	}
}

func TestUnauthorizedRereadsEndpointOnce(t *testing.T) {
	srv := httptest.NewServer(pingWrap("miaoju-desktop", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	})))
	defer srv.Close()
	calls := 0
	c := New(Options{Resolve: func() (endpoint.Info, error) {
		calls++
		tok := "stale-token"
		if calls > 1 {
			tok = testCLIToken // 应用重启后文件已重写
		}
		return infoFor(t, srv, tok, ""), nil
	}})
	if _, err := c.ListProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("Resolve 调用 %d 次，应为 2（401 后重读一次）", calls)
	}
}

func TestUnauthorizedTwiceIsAppNotRunning(t *testing.T) {
	srv := httptest.NewServer(pingWrap("miaoju-desktop", http.NotFoundHandler()))
	defer srv.Close()
	calls := 0
	c := New(Options{Resolve: func() (endpoint.Info, error) { calls++; return infoFor(t, srv, "wrong", ""), nil }})
	_, err := c.ListProjects(context.Background())
	var anr *AppNotRunningError
	if !errors.As(err, &anr) || calls != 2 {
		t.Fatalf("err=%T calls=%d", err, calls)
	}
}

func TestVersionMismatchWarnsOnce(t *testing.T) {
	srv := httptest.NewServer(pingWrap("miaoju-desktop", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	})))
	defer srv.Close()
	var warns []string
	info := infoFor(t, srv, testCLIToken, "2.0.0")
	c := New(Options{Resolve: func() (endpoint.Info, error) { return info, nil }, Version: "1.0.0",
		Warn: func(s string) { warns = append(warns, s) }})
	_, _ = c.ListProjects(context.Background())
	_, _ = c.ListProjects(context.Background())
	if len(warns) != 1 || !strings.Contains(warns[0], "2.0.0") {
		t.Fatalf("warns=%v", warns)
	}
}

func pingServer(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	info := infoFor(t, srv, testCLIToken, "9.9.9")
	return New(Options{Resolve: func() (endpoint.Info, error) { return info, nil }, Version: "9.9.9"})
}

// 端口上不是喵剧（应用崩溃后端口被别的程序占用）：ping 404 / 200 HTML 都按未运行处理。
func TestPingNon401ErrorMeansAppNotRunning(t *testing.T) {
	c := pingServer(t, http.NotFound)
	_, err := c.ListProjects(context.Background())
	var anr *AppNotRunningError
	if !errors.As(err, &anr) {
		t.Fatalf("ping 404 应为 AppNotRunningError，got %T %v", err, err)
	}
}

func TestPingHTMLBodyMeansAppNotRunning(t *testing.T) {
	c := pingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>hello</html>"))
	})
	_, err := c.ListProjects(context.Background())
	var anr *AppNotRunningError
	if !errors.As(err, &anr) {
		t.Fatalf("ping 200 HTML 应为 AppNotRunningError，got %T %v", err, err)
	}
}

func TestDownloadURLConnectionRefusedIsClassified(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	c := pingServer(t, http.NotFound)
	_, err := c.DownloadURL(context.Background(), u, &strings.Builder{})
	var anr *AppNotRunningError
	if !errors.As(err, &anr) {
		t.Fatalf("连接被拒应经 classifyNetErr 归为 AppNotRunningError，got %T %v", err, err)
	}
}

// 应用崩溃重启后端口变了：旧端口连接被拒（不是 401），已连上的 client 应重读地址文件换到新端口。
func TestConnRefusedAfterConnectedRereadsEndpointOnce(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	})
	a := httptest.NewServer(pingWrap("miaoju-desktop", ok))
	b := httptest.NewServer(pingWrap("miaoju-desktop", ok))
	defer b.Close()
	cur := infoFor(t, a, testCLIToken, "")
	calls := 0
	c := New(Options{Resolve: func() (endpoint.Info, error) { calls++; return cur, nil }})
	if _, err := c.ListProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.Close()
	cur = infoFor(t, b, testCLIToken, "")
	if _, err := c.ListProjects(context.Background()); err != nil {
		t.Fatalf("端口变了应重连新端口，err=%T %v", err, err)
	}
	if calls != 2 {
		t.Fatalf("Resolve 调用 %d 次，应为 2", calls)
	}
}

func TestConnRefusedSamePortReturnsOriginalError(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	})
	a := httptest.NewServer(pingWrap("miaoju-desktop", ok))
	info := infoFor(t, a, testCLIToken, "")
	c := New(Options{Resolve: func() (endpoint.Info, error) { return info, nil }})
	if _, err := c.ListProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.Close()
	_, err := c.ListProjects(context.Background())
	var anr *AppNotRunningError
	if !errors.As(err, &anr) || !isConnRefused(anr.Err) {
		t.Fatalf("端口没变应返回原来的连接被拒，err=%T %v", err, err)
	}
}

func TestConnRefusedReconnectsAtMostOncePerCommand(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	})
	a := httptest.NewServer(pingWrap("miaoju-desktop", ok))
	b := httptest.NewServer(pingWrap("miaoju-desktop", ok))
	cc := httptest.NewServer(pingWrap("miaoju-desktop", ok))
	defer cc.Close()
	cur := infoFor(t, a, testCLIToken, "")
	c := New(Options{Resolve: func() (endpoint.Info, error) { return cur, nil }})
	if _, err := c.ListProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.Close()
	cur = infoFor(t, b, testCLIToken, "")
	if _, err := c.ListProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.Close()
	cur = infoFor(t, cc, testCLIToken, "")
	_, err := c.ListProjects(context.Background())
	var anr *AppNotRunningError
	if !errors.As(err, &anr) {
		t.Fatalf("同一条命令只重连一次，第二次端口变化应直接报错，err=%T %v", err, err)
	}
}

// 非契约错误体（如网关的中文 HTML）截断时不能切出半个 UTF-8 字符。
func TestDecodeAPIErrorTruncatesOnRuneBoundary(t *testing.T) {
	body := []byte("x" + strings.Repeat("错", 100))
	err := decodeAPIError(&http.Response{StatusCode: 502, Status: "502 Bad Gateway", Header: http.Header{}}, body)
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("got %T", err)
	}
	if !utf8.ValidString(ae.Message) || !strings.HasSuffix(ae.Message, "…") || len(ae.Message) > 200+len("…") {
		t.Fatalf("截断结果 = %q", ae.Message)
	}
}

// 上传的 multipart 里是文件原样内容与文件名（文件流式写进缓冲区，不再先整份读进内存）。
func TestUploadAssetSendsFileAsMultipart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "参考 图.png")
	content := strings.Repeat("\x89PNG-data-", 1000)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotName, gotBody string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		f, fh, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile: %v", err)
			return
		}
		b, _ := io.ReadAll(f)
		gotName, gotBody = fh.Filename, string(b)
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	if _, err := c.UploadAsset(context.Background(), 3, path); err != nil {
		t.Fatal(err)
	}
	if gotName != "参考 图.png" || gotBody != content {
		t.Fatalf("name=%q bodyLen=%d", gotName, len(gotBody))
	}
}
