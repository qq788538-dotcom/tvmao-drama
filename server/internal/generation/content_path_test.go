package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/upstream"
)

// contentPath 产物（上游只给带鉴权的内容地址）流式落 spill 临时文件，以 file:// 交给落盘，
// 不再整份读进内存转 base64 data URL（P34）。
func contentPathServer(t *testing.T, video []byte, contentType string) (*httptest.Server, *int32, *atomic.Value) {
	t.Helper()
	var contentHits int32
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			atomic.AddInt32(&contentHits, 1)
			auth.Store(r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", contentType)
			_, _ = w.Write(video)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &contentHits, &auth
}

const contentPathTemplate = `{"path":"/v1/videos/{{task_id}}","contentPath":"/v1/videos/{{task_id}}/content","interval":1,"maxWait":60}`

func TestPollUpstreamContentPathStreamsToSpillFile(t *testing.T) {
	video := append([]byte("\x00\x00\x00\x18ftypmp42"), bytes.Repeat([]byte{3}, 32<<10)...)
	srv, hits, auth := contentPathServer(t, video, "application/octet-stream")
	tmp := t.TempDir()
	svc := &Service{httpClient: srv.Client(), tmpDir: tmp}
	client := &pollClient{Client: upstream.NewClient(upstream.ClientOptions{BaseURL: srv.URL}), svc: svc, baseURL: srv.URL + "/"}

	res, err := svc.pollUpstream(context.Background(), client, "sk-x", json.RawMessage(contentPathTemplate), "vid1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(hits) != 1 || auth.Load() != "Bearer sk-x" {
		t.Fatalf("内容地址应带鉴权下载一次, hits=%d auth=%v", atomic.LoadInt32(hits), auth.Load())
	}
	if len(res.OutputURLs) != 1 || !strings.HasPrefix(res.OutputURLs[0], "file://") {
		t.Fatalf("产物应以 file:// 交给落盘, got %v", res.OutputURLs)
	}
	path := strings.TrimPrefix(res.OutputURLs[0], "file://")
	if _, err := svc.resolveSpillOutputPath(path); err != nil {
		t.Fatalf("落盘应接受该临时文件: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, video) {
		t.Fatal("文件内容与上游不一致")
	}
	if filepath.Dir(path) != tmp {
		t.Fatalf("应落在 tempDir: %s", path)
	}

	// 落盘照常：file:// 分支按内容嗅探 mime，落完由 RemoveSpillFiles 删除临时文件。
	st := &captureStorage{}
	svc.storage = st
	urls, _, mimes, _, err := svc.persistOutputs(context.Background(), 1, 5, nil, res.OutputURLs, 0)
	if err != nil || !strings.HasPrefix(urls[0], "media://") || mimes[0] != "video/mp4" {
		t.Fatalf("persist: urls=%v mimes=%v err=%v", urls, mimes, err)
	}
	res.RemoveSpillFiles()
	assertDirEmpty(t, tmp)
}

// 超过单文件上限是确定性的：直接失败，不在轮询窗口里反复重下。
func TestPollUpstreamContentPathTooLargeFailsFast(t *testing.T) {
	srv, hits, _ := contentPathServer(t, bytes.Repeat([]byte{1}, 4096), "video/mp4")
	tmp := t.TempDir()
	svc := &Service{httpClient: srv.Client(), tmpDir: tmp, maxMediaOutputBytes: 1024}
	client := &pollClient{Client: upstream.NewClient(upstream.ClientOptions{BaseURL: srv.URL}), svc: svc, baseURL: srv.URL}

	_, err := svc.pollUpstream(context.Background(), client, "sk-x", json.RawMessage(contentPathTemplate), "vid1", 0)
	if !errors.Is(err, errOutputTooLarge) {
		t.Fatalf("应报超限, got %v", err)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Fatalf("超限不应重下, hits=%d", atomic.LoadInt32(hits))
	}
	assertDirEmpty(t, tmp)
}
