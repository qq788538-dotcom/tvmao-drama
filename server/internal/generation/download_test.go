package generation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
)

// countingServer 每次请求计数，按 handler 回包。
func countingServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// assertDirEmpty：下载临时文件无论成功失败都必须删掉。
func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("临时目录应为空, 残留 %v", names)
	}
}

func TestPersistOutputsStreamsRemoteDownloadAndRemovesTempFile(t *testing.T) {
	body := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 64<<10)...)
	srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	})
	tmp := t.TempDir()
	st := &captureStorage{}
	svc := &Service{storage: st, httpClient: srv.Client(), tmpDir: tmp}
	urls, _, mimes, errs, err := svc.persistOutputs(context.Background(), 1, 90, nil, []string{srv.URL + "/a.png"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if urls[0] != "media://u/gen/task-90-0" || mimes[0] != "image/png" || errs != nil {
		t.Fatalf("url=%s mime=%s errs=%v", urls[0], mimes[0], errs)
	}
	if !bytes.Equal(st.got["u/gen/task-90-0"], body) || st.sizes["u/gen/task-90-0"] != int64(len(body)) {
		t.Fatal("上传字节/长度必须与下载内容逐字相同")
	}
	assertDirEmpty(t, tmp)
}

// 没有 Content-Length 的流靠 LimitReader 截住；超限不重试，失败原因进 persistErrors。
func TestPersistOutputsRejectsOversizedStreamWithoutRetry(t *testing.T) {
	zeroPersistRetryDelays(t)
	srv, hits := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		for i := 0; i < 8; i++ {
			_, _ = w.Write(bytes.Repeat([]byte{1}, 1024))
			w.(http.Flusher).Flush() // 强制 chunked，不带 Content-Length
		}
	})
	tmp := t.TempDir()
	st := &captureStorage{}
	svc := &Service{storage: st, httpClient: srv.Client(), tmpDir: tmp}
	svc.SetDownloadLimits(1024, 4096)
	raw := srv.URL + "/v.mp4"
	urls, ids, _, errs, err := svc.persistOutputs(context.Background(), 1, 91, nil, []string{raw}, 0)
	if err != nil {
		t.Fatalf("http 产物超限不应让任务失败: %v", err)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Fatalf("超限是确定性错误，不应重试, hits=%d", *hits)
	}
	if urls[0] != raw || ids[0] != 0 || len(st.got) != 0 {
		t.Fatalf("应保留上游地址且不上传, url=%s id=%d", urls[0], ids[0])
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "产物超过大小上限") {
		t.Fatalf("失败原因应落到 persistErrors: %v", errs)
	}
	assertDirEmpty(t, tmp)
}

// Content-Length 已超限：不落盘直接拒绝。
func TestDownloadToTempRejectsByContentLength(t *testing.T) {
	srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", strconv.Itoa(8192))
		_, _ = w.Write(make([]byte, 8192))
	})
	tmp := t.TempDir()
	svc := &Service{httpClient: srv.Client(), tmpDir: tmp}
	svc.SetDownloadLimits(1024, 4096)
	_, _, _, err := svc.downloadToTemp(context.Background(), srv.URL)
	if !errors.Is(err, errOutputTooLarge) || isRetryablePersistError(err) {
		t.Fatalf("应按 Content-Length 判超限且不可重试: %v", err)
	}
	assertDirEmpty(t, tmp)
}

// 图片与音视频分别用各自上限：同样 2KB，图片超限、视频放行。
func TestDownloadToTempLimitByKind(t *testing.T) {
	payload := make([]byte, 2048)
	for _, tc := range []struct {
		ct     string
		wantOK bool
	}{{"image/png", false}, {"video/mp4", true}} {
		srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", tc.ct)
			_, _ = w.Write(payload)
		})
		svc := &Service{httpClient: srv.Client(), tmpDir: t.TempDir()}
		svc.SetDownloadLimits(1024, 4096)
		rc, n, _, err := svc.downloadToTemp(context.Background(), srv.URL)
		if tc.wantOK {
			if err != nil || n != 2048 {
				t.Fatalf("%s: n=%d err=%v", tc.ct, n, err)
			}
			rc.Close()
		} else if !errors.Is(err, errOutputTooLarge) {
			t.Fatalf("%s: 应超限, got %v", tc.ct, err)
		}
	}
}

// 空 / octet-stream 的 Content-Type 按前 512 字节嗅探；嗅探出图片后按图片上限复核。
func TestDownloadToTempSniffsMissingContentType(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 600)...)
	for name, set := range map[string]func(http.Header){
		"空":            func(h http.Header) { h["Content-Type"] = nil }, // 阻止 net/http 服务端自动嗅探补头
		"octet-stream": func(h http.Header) { h.Set("Content-Type", "application/octet-stream") },
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
				set(w.Header())
				_, _ = w.Write(png)
			})
			tmp := t.TempDir()
			svc := &Service{httpClient: srv.Client(), tmpDir: tmp}
			rc, n, mime, err := svc.downloadToTemp(context.Background(), srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(rc)
			if mime != "image/png" || n != int64(len(png)) || !bytes.Equal(got, png) {
				t.Fatalf("mime=%q n=%d", mime, n)
			}
			if err := rc.Close(); err != nil {
				t.Fatal(err)
			}
			assertDirEmpty(t, tmp)

			svc.SetDownloadLimits(512, 4096)
			if _, _, _, err := svc.downloadToTemp(context.Background(), srv.URL); !errors.Is(err, errOutputTooLarge) {
				t.Fatalf("嗅探为图片后应按图片上限判超限: %v", err)
			}
			assertDirEmpty(t, tmp)
		})
	}
}

// 4xx（除 408/429）不重试；408/429/5xx 重试。
func TestPersistRetryClassifiesStatus(t *testing.T) {
	zeroPersistRetryDelays(t)
	for _, tc := range []struct {
		status   int
		wantHits int32
	}{{404, 1}, {403, 1}, {408, 4}, {429, 4}, {503, 4}} {
		srv, hits := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "x", tc.status)
		})
		svc := &Service{storage: &captureStorage{}, httpClient: srv.Client(), tmpDir: t.TempDir()}
		_, _, _, errs, err := svc.persistOutputs(context.Background(), 1, 92, nil, []string{srv.URL + "/a"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := atomic.LoadInt32(hits); got != tc.wantHits {
			t.Fatalf("status %d: hits=%d want %d", tc.status, got, tc.wantHits)
		}
		if len(errs) != 1 || !strings.Contains(errs[0], strconv.Itoa(tc.status)) {
			t.Fatalf("status %d: persistErrors=%v", tc.status, errs)
		}
	}
}

// 全局下载闸：并发落盘的 http 产物数不超过容量。
func TestDownloadSemaphoreLimitsConcurrentDownloads(t *testing.T) {
	var inflight, peak int32
	srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&inflight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt32(&inflight, -1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nDATA"))
	})
	svc := &Service{storage: &lockedCaptureStorage{}, httpClient: srv.Client(), tmpDir: t.TempDir()}
	svc.SetDownloadConcurrency(2)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, _, _, err := svc.persistSlot(context.Background(), 1, 93, 0, i, srv.URL+"/a.png", 0); err != nil {
				t.Errorf("slot %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if p := atomic.LoadInt32(&peak); p < 1 || p > 2 {
		t.Fatalf("并发下载峰值应 <= 2, got %d", p)
	}
}

// 名额等待感知 ctx：闸满时 ctx 取消立即返回。
func TestAcquireDownloadHonorsContext(t *testing.T) {
	svc := &Service{}
	svc.SetDownloadConcurrency(1)
	release, err := svc.acquireDownload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := svc.acquireDownload(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("闸满时应随 ctx 超时返回, got %v", err)
	}
	release()
	release() // 重复归还无副作用
	r2, err := svc.acquireDownload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r2()
}

// lockedCaptureStorage：并发 PutObject 用（captureStorage 的 map 不是并发安全的）。
type lockedCaptureStorage struct {
	mu sync.Mutex
	captureStorage
}

func (l *lockedCaptureStorage) PutObject(ctx context.Context, in assets.PutObjectInput) (assets.StoredObject, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.captureStorage.PutObject(ctx, in)
}
