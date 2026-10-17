package aicharacters

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

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

// newCacheFixture 用内存 FS 代替内置图片目录，返回缓存和对象存储。
func newCacheFixture(t *testing.T, src fs.FS) (*ImageCache, *memObjectStore) {
	t.Helper()
	conn := dbtest.Open(t)
	seedCharacters(t, conn)
	store := newMemObjectStore()
	return NewImageCache(NewSQLiteRepository(conn), src, store), store
}

const charKey = "builtin/characters/asset-001.jpg"

func fixtureFS() fstest.MapFS {
	return fstest.MapFS{"asset-001.jpg": {Data: []byte("JPEGDATA")}}
}

func TestEnsureCopiesEmbeddedImageOnce(t *testing.T) {
	c, store := newCacheFixture(t, fixtureFS())
	handled, err := c.Ensure(context.Background(), charKey)
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if string(store.objs[charKey]) != "JPEGDATA" || store.types[charKey] != "image/jpeg" {
		t.Fatalf("缓存内容不对: %q %q", store.objs[charKey], store.types[charKey])
	}
	// 已在本地：不再复制。
	handled, err = c.Ensure(context.Background(), charKey)
	if err != nil || handled {
		t.Fatalf("第二次 handled=%v err=%v", handled, err)
	}
}

func TestEnsureIgnoresNonCharacterKeys(t *testing.T) {
	c, store := newCacheFixture(t, fixtureFS())
	handled, err := c.Ensure(context.Background(), "u1/2026/10/mine.png")
	if err != nil || handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if len(store.objs) != 0 {
		t.Fatal("非角色图不应落盘")
	}
}

func TestEnsureMissingEmbeddedImage(t *testing.T) {
	c, store := newCacheFixture(t, fstest.MapFS{})
	handled, err := c.Ensure(context.Background(), charKey)
	if !handled || err == nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if len(store.objs) != 0 {
		t.Fatal("缺图不应留下缓存")
	}
}

func TestMiddlewareCachesBeforeServing(t *testing.T) {
	c, store := newCacheFixture(t, fixtureFS())
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

	// 非 GET/HEAD 不触发复制，直接交给原 handler。
	delete(store.objs, charKey)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/media/"+charKey, nil))
	if store.Exists(context.Background(), charKey) {
		t.Fatal("POST 不应触发复制")
	}
}

// 种子里每个角色都必须有对应的内置 WebP，且扩展名一致——/media 按扩展名出
// Content-Type 并带 nosniff，扩展名和内容对不上图就显示不出来。
func TestEverySeedCharacterHasEmbeddedWebP(t *testing.T) {
	raw := requirePack(t)
	imgs := EmbeddedImages()
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() {
		var row struct {
			ImageURL string `json:"image_url"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		key := strings.TrimPrefix(row.ImageURL, "media://")
		if path.Ext(key) != ".webp" {
			t.Fatalf("%s 不是 .webp", row.ImageURL)
		}
		body, err := fs.ReadFile(imgs, path.Base(key))
		if err != nil {
			t.Fatalf("缺少内置图 %s: %v", key, err)
		}
		if ct := http.DetectContentType(body); ct != "image/webp" {
			t.Fatalf("%s 内容类型 %s", key, ct)
		}
		n++
	}
	if n == 0 {
		t.Fatal("种子为空")
	}
}
