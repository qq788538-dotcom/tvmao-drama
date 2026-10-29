package generation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

func setIdleTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := downloadIdleTimeout
	downloadIdleTimeout = d
	t.Cleanup(func() { downloadIdleTimeout = old })
}

// 慢但一直在来字节的下载不应被掐断：总耗时远超空闲阈值也能成功（P24）。
func TestDownloadToTempSlowButSteadySucceeds(t *testing.T) {
	setIdleTimeout(t, 150*time.Millisecond)
	chunk := bytes.Repeat([]byte{1}, 1024)
	srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		fl := w.(http.Flusher)
		for i := 0; i < 12; i++ { // 约 600ms，是空闲阈值的 4 倍
			_, _ = w.Write(chunk)
			fl.Flush()
			time.Sleep(50 * time.Millisecond)
		}
	})
	tmp := t.TempDir()
	svc := &Service{httpClient: srv.Client(), tmpDir: tmp}
	rc, n, _, err := svc.downloadToTemp(context.Background(), srv.URL+"/v")
	if err != nil {
		t.Fatalf("持续有数据的慢下载应成功: %v", err)
	}
	_ = rc.Close()
	if n != int64(12*len(chunk)) {
		t.Fatalf("n=%d", n)
	}
	assertDirEmpty(t, tmp)
}

// 读 body 期间长时间一个字节都没有：按空闲超时取消，错误可重试，临时文件删掉。
func TestDownloadToTempIdleStallCancels(t *testing.T) {
	setIdleTimeout(t, 100*time.Millisecond)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("head"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	tmp := t.TempDir()
	svc := &Service{httpClient: srv.Client(), tmpDir: tmp}
	start := time.Now()
	_, _, _, err := svc.downloadToTemp(context.Background(), srv.URL+"/v")
	if !errors.Is(err, errDownloadIdle) {
		t.Fatalf("应报读空闲超时, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("空闲超时应及时生效, 用时 %v", time.Since(start))
	}
	if !isRetryablePersistError(err) {
		t.Fatal("读空闲超时是瞬时错误，应重试")
	}
	assertDirEmpty(t, tmp)
}

// 参考图下载共用同一套空闲超时。
func TestFetchInputImageIdleStallCancels(t *testing.T) {
	setIdleTimeout(t, 100*time.Millisecond)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	svc := &Service{httpClient: srv.Client()}
	if _, _, err := svc.fetchInputImage(context.Background(), srv.URL+"/a.png"); !errors.Is(err, errDownloadIdle) {
		t.Fatalf("应报读空闲超时, got %v", err)
	}
}

// 生产 client 不再有整请求时限，只在 Transport 上卡响应头等待。
func TestNewDownloadHTTPClientHasNoWholeRequestTimeout(t *testing.T) {
	c := newDownloadHTTPClient()
	if c.Timeout != 0 {
		t.Fatalf("不应有整请求时限, got %v", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.ResponseHeaderTimeout != downloadHeaderTimeout {
		t.Fatalf("应设置 ResponseHeaderTimeout, got %#v", c.Transport)
	}
	if NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).httpClient.Timeout != 0 {
		t.Fatal("NewService 的下载 client 不应有整请求时限")
	}
}

// 读完后 Close 停计时器：之后不会再触发取消（无泄漏的 goroutine/计时器副作用可观测为 ctx 未带 cause）。
func TestIdleTimeoutBodyCloseStopsTimer(t *testing.T) {
	setIdleTimeout(t, 50*time.Millisecond)
	srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	svc := &Service{httpClient: srv.Client()}
	resp, err := svc.getWithIdleTimeout(context.Background(), srv.URL, http.Header{"X-Test": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	body := resp.Body.(*idleTimeoutBody)
	time.Sleep(120 * time.Millisecond)
	if string(b) != "ok" || errors.Is(context.Cause(body.ctx), errDownloadIdle) {
		t.Fatalf("Close 后不应再按空闲超时取消, body=%q cause=%v", b, context.Cause(body.ctx))
	}
}
