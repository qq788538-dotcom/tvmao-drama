package generation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
)

func zeroPersistRetryDelays(t *testing.T) {
	t.Helper()
	old := persistRetryDelays
	persistRetryDelays = []time.Duration{0, 0, 0}
	t.Cleanup(func() { persistRetryDelays = old })
}

// 前 failures 次返回 500，之后返回 PNG。
func flakyServer(t *testing.T, failures int32) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) <= failures {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nDATA"))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestPersistOutputsRetriesRemoteDownloadThreeTimes(t *testing.T) {
	zeroPersistRetryDelays(t)
	srv, hits := flakyServer(t, 3)
	st := &captureStorage{}
	svc := &Service{storage: st, httpClient: srv.Client()}
	urls, ids, mimes, err := svc.persistOutputs(context.Background(), 1, 77, nil, []string{srv.URL + "/a.png"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(hits) != 4 {
		t.Fatalf("首次 + 重试 3 次 = 4 次请求, got %d", *hits)
	}
	if !strings.HasPrefix(urls[0], "media://") || mimes[0] != "image/png" {
		t.Fatalf("第 4 次成功后应落本地, url=%s mime=%s", urls[0], mimes[0])
	}
	_ = ids
}

func TestPersistOutputsGivesUpAfterThreeRetries(t *testing.T) {
	zeroPersistRetryDelays(t)
	srv, hits := flakyServer(t, 100)
	svc := &Service{storage: &captureStorage{}, httpClient: srv.Client()}
	raw := srv.URL + "/a.png?x=1&y=2"
	urls, ids, _, err := svc.persistOutputs(context.Background(), 1, 78, nil, []string{raw}, 0)
	if err != nil {
		t.Fatalf("http 产物下载失败不应让任务失败: %v", err)
	}
	if atomic.LoadInt32(hits) != 4 {
		t.Fatalf("应恰好尝试 4 次, got %d", *hits)
	}
	if urls[0] != raw || ids[0] != 0 {
		t.Fatalf("应保留上游地址且 assetId=0, url=%s id=%d", urls[0], ids[0])
	}
}

func TestPersistOutputsDoesNotRetryDataURL(t *testing.T) {
	zeroPersistRetryDelays(t)
	st := &countingFailStorage{}
	svc := &Service{storage: st}
	_, _, _, _ = svc.persistOutputs(context.Background(), 1, 79, nil, []string{"data:image/png;base64,aGk="}, 0)
	if st.puts != 1 {
		t.Fatalf("data: 产物失败是确定性的，不应重试, puts=%d", st.puts)
	}
}

type countingFailStorage struct {
	fakeInputMediaStorage
	puts int
}

func (c *countingFailStorage) PutObject(context.Context, assets.PutObjectInput) (assets.StoredObject, error) {
	c.puts++
	return assets.StoredObject{}, errors.New("disk full")
}
