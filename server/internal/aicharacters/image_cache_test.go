package aicharacters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

type memObjectStore struct {
	mu    sync.Mutex
	objs  map[string][]byte
	types map[string]string
}

func newMemObjectStore() *memObjectStore {
	return &memObjectStore{objs: map[string][]byte{}, types: map[string]string{}}
}

func (m *memObjectStore) Exists(_ context.Context, key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objs[key]
	return ok
}

func (m *memObjectStore) Put(_ context.Context, key, contentType string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[key] = body
	m.types[key] = contentType
	return nil
}

// newCacheFixture 起一个假“图片站”，返回缓存、对象存储和命中计数。
func newCacheFixture(t *testing.T, handler http.HandlerFunc, base func(srvURL string) string) (*ImageCache, *memObjectStore, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	conn := dbtest.Open(t)
	seedCharacters(t, conn)
	store := newMemObjectStore()
	b := base(srv.URL)
	get := func(key string) (string, error) {
		if key == SettingImageBaseURL {
			return b, nil
		}
		return "", nil
	}
	return NewImageCache(NewSQLiteRepository(conn), get, store), store, &hits
}

func jpegHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/tapnow/sd-assets/asset-001.jpg" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Write([]byte("JPEGDATA"))
}

const charKey = "tapnow/sd-assets/asset-001.jpg"

func TestEnsureDownloadsCharacterImageOnce(t *testing.T) {
	c, store, hits := newCacheFixture(t, jpegHandler, func(u string) string { return u + "/" })
	handled, err := c.Ensure(context.Background(), charKey)
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if string(store.objs[charKey]) != "JPEGDATA" || store.types[charKey] != "image/jpeg" {
		t.Fatalf("缓存内容不对: %q %q", store.objs[charKey], store.types[charKey])
	}
	// 已缓存：不再下载（断网也能显示）。
	handled, err = c.Ensure(context.Background(), charKey)
	if err != nil || handled {
		t.Fatalf("第二次 handled=%v err=%v", handled, err)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Fatalf("下载次数 = %d, want 1", *hits)
	}
}

func TestEnsureIgnoresNonCharacterKeys(t *testing.T) {
	c, store, hits := newCacheFixture(t, jpegHandler, func(u string) string { return u })
	handled, err := c.Ensure(context.Background(), "u1/2026/10/mine.png")
	if err != nil || handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if len(store.objs) != 0 || atomic.LoadInt32(hits) != 0 {
		t.Fatal("非角色图不应下载")
	}
}

func TestEnsureBaseNotConfigured(t *testing.T) {
	c, store, hits := newCacheFixture(t, jpegHandler, func(string) string { return "  " })
	handled, err := c.Ensure(context.Background(), charKey)
	if !handled || !errors.Is(err, ErrImageBaseNotConfigured) {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if len(store.objs) != 0 || atomic.LoadInt32(hits) != 0 {
		t.Fatal("未配置时不应下载或落盘")
	}
}

func TestEnsureRemoteErrorNotCached(t *testing.T) {
	c, store, _ := newCacheFixture(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		func(u string) string { return u })
	if _, err := c.Ensure(context.Background(), charKey); err == nil {
		t.Fatal("远端 404 应返回错误")
	}
	if len(store.objs) != 0 {
		t.Fatal("失败不应留下缓存")
	}
}

func TestEnsureRejectsOversize(t *testing.T) {
	old := maxImageBytes
	maxImageBytes = 4
	t.Cleanup(func() { maxImageBytes = old })
	c, store, _ := newCacheFixture(t, jpegHandler, func(u string) string { return u })
	if _, err := c.Ensure(context.Background(), charKey); err == nil {
		t.Fatal("超过上限应返回错误")
	}
	if len(store.objs) != 0 {
		t.Fatal("超限不应留下缓存")
	}
}

// 网关可能回 application/octet-stream：按扩展名兜底，P1b 的 /media 才能给出正确 Content-Type。
func TestEnsureContentTypeFallsBackToExtension(t *testing.T) {
	c, store, _ := newCacheFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("PNGDATA"))
	}, func(u string) string { return u })
	key := "tapnow/sd-assets/asset-003.png"
	if _, err := c.Ensure(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if store.types[key] != "image/png" {
		t.Fatalf("content type = %q, want image/png", store.types[key])
	}
}

func TestMiddlewareCachesBeforeServing(t *testing.T) {
	c, store, hits := newCacheFixture(t, jpegHandler, func(u string) string { return u })
	var sawCached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawCached = store.Exists(r.Context(), charKey)
		w.WriteHeader(http.StatusOK)
	})
	h := c.Middleware(next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/"+charKey+"?token=t", nil))
	if !sawCached || rec.Code != http.StatusOK {
		t.Fatalf("原 handler 执行时应已缓存: sawCached=%v code=%d", sawCached, rec.Code)
	}

	// 非 GET/HEAD 不触发下载，直接交给原 handler。
	delete(store.objs, charKey)
	before := atomic.LoadInt32(hits)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/media/"+charKey, nil))
	if atomic.LoadInt32(hits) != before {
		t.Fatal("POST 不应触发下载")
	}
}
