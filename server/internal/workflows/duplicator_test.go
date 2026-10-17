package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
)

// 这组测试盯的是并发化之后最容易悄悄坏掉的那件事：**失败路径上一个已复制对象都不能漏
// 出 GC 队列**。漏掉的对象没有任何 asset 行引用它，谁也不会再去删，就是永久的存储泄漏
// ——而且完全无声，只能靠账单发现。断言一律用「GC 队列 == storage 实际写出的 dstKey
// 集合」这种集合相等，而不是数个数：并发调度顺序不确定，只有集合相等才不受调度影响。

type fakeCopyStorage struct {
	assets.Storage
	mu sync.Mutex
	// copied 记下每次成功 CopyObject 写出的 dstKey——回滚断言的黄金标准。
	copied  []string
	failOn  map[string]bool // srcKey → 该次 CopyObject 直接失败
	prefix  string
	nextIdx int
}

func newFakeCopyStorage() *fakeCopyStorage {
	return &fakeCopyStorage{failOn: map[string]bool{}, prefix: "media://"}
}

func (s *fakeCopyStorage) CopyObject(_ context.Context, srcKey, dstKey, _ string) (assets.StoredObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOn[srcKey] {
		return assets.StoredObject{}, fmt.Errorf("copy failed for %s", srcKey)
	}
	s.copied = append(s.copied, dstKey)
	return assets.StoredObject{URL: s.prefix + dstKey, Size: 100}, nil
}

func (s *fakeCopyStorage) ExtractKey(urlOrKey string) (string, bool) {
	if len(urlOrKey) > len(s.prefix) && urlOrKey[:len(s.prefix)] == s.prefix {
		return urlOrKey[len(s.prefix):], true
	}
	return "", false
}

func (s *fakeCopyStorage) copiedKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]string(nil), s.copied...)
	sort.Strings(out)
	return out
}

type fakeAssetsRepo struct {
	assets.Repository
	mu           sync.Mutex
	insertFailOn map[string]bool // 该 objectKey 的 Insert 失败
	insertAll    bool            // 所有 Insert 都失败——制造「已复制但没入库」
	nextID       int64
	inserted     []assets.AssetRecord
}

func (r *fakeAssetsRepo) ListByObjectKey(objectKey string) ([]assets.AssetRecord, error) {
	return []assets.AssetRecord{{
		ID: 1, ObjectKey: objectKey, URL: "media://" + objectKey,
		SizeBytes: 100, MimeType: "image/png",
	}}, nil
}

// 目标项目恒为空——测试不覆盖 hash 去重命中（并发下它本就是尽力而为，见 copyOne 注释）。
func (r *fakeAssetsRepo) FindByUserProjectHash(int64, int64, string) (*assets.AssetRecord, error) {
	return nil, nil
}

func (r *fakeAssetsRepo) Insert(rec assets.AssetRecord) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.insertAll || r.insertFailOn[rec.ObjectKey] {
		return 0, fmt.Errorf("insert failed for %s", rec.ObjectKey)
	}
	r.nextID++
	r.inserted = append(r.inserted, rec)
	return r.nextID, nil
}

type fakeStorageAdjuster struct {
	mu    sync.Mutex
	delta int64
}

func (f *fakeStorageAdjuster) AddStorage(_ int64, delta int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delta += delta
	return nil
}

type fakeGC struct {
	mu   sync.Mutex
	keys []string
}

func (g *fakeGC) EnqueueObjectKey(key string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.keys = append(g.keys, key)
	return nil
}

func (g *fakeGC) sorted() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := append([]string(nil), g.keys...)
	sort.Strings(out)
	return out
}

func snapshotWithKeys(n int) json.RawMessage {
	nodes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		nodes = append(nodes, fmt.Sprintf(`{"id":"n%d","content":"media://src/%d.png"}`, i, i))
	}
	return json.RawMessage(`{"nodes":[` + join(nodes, ",") + `]}`)
}

func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

func newTestDuplicator(storage *fakeCopyStorage, repo *fakeAssetsRepo, gc *fakeGC) *AssetDuplicator {
	return NewAssetDuplicator(storage, repo, &fakeStorageAdjuster{}, gc)
}

// 并发跑完后：每个源 key 都被复制一次、快照里的 URL 全部指向新 key、字节数正确累加。
func TestDuplicateConcurrentCopiesEveryKey(t *testing.T) {
	const n = 20
	storage := newFakeCopyStorage()
	repo := &fakeAssetsRepo{insertFailOn: map[string]bool{}}
	gc := &fakeGC{}
	d := newTestDuplicator(storage, repo, gc)

	res, err := d.Duplicate(context.Background(), 7, 42, "project_duplicate", snapshotWithKeys(n))
	if err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	if got := len(storage.copiedKeys()); got != n {
		t.Fatalf("CopyObject 次数 = %d, want %d", got, n)
	}
	if got := len(res.NewAssetIDs); got != n {
		t.Fatalf("NewAssetIDs = %d, want %d", got, n)
	}
	if res.BytesCopied != int64(n*100) {
		t.Fatalf("BytesCopied = %d, want %d", res.BytesCopied, n*100)
	}
	// 成功路径一个对象都不该进 GC 队列。
	if got := gc.sorted(); len(got) != 0 {
		t.Fatalf("成功路径不该有对象进 GC: %v", got)
	}
	// 快照里不能再出现任何源 key——全部被重写成新 key。
	for i := 0; i < n; i++ {
		if contains(string(res.Snapshot), fmt.Sprintf("media://src/%d.png", i)) {
			t.Fatalf("快照仍指向源 key %d: %s", i, res.Snapshot)
		}
	}
}

// CopyObject 中途失败：已经写出去的对象必须一个不落地进 GC 队列。
func TestDuplicateRollbackEnqueuesEveryCopiedObject(t *testing.T) {
	const n = 20
	storage := newFakeCopyStorage()
	storage.failOn["src/7.png"] = true
	repo := &fakeAssetsRepo{insertFailOn: map[string]bool{}}
	gc := &fakeGC{}
	d := newTestDuplicator(storage, repo, gc)

	_, err := d.Duplicate(context.Background(), 7, 42, "project_duplicate", snapshotWithKeys(n))
	if err == nil {
		t.Fatal("期望 Duplicate 返回错误")
	}
	// 黄金标准：GC 队列 == storage 实际写出的对象集合。并发调度决定了到底有几个 worker
	// 在失败前跑完，所以不能断言个数——但「写出去多少就回收多少」必须恒成立。
	want := storage.copiedKeys()
	got := gc.sorted()
	if len(got) != len(want) {
		t.Fatalf("GC 队列 %d 个对象，实际写出 %d 个——漏回收即静默存储泄漏\ngc=%v\ncopied=%v",
			len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("GC 队列与已复制对象不一致:\ngc=%v\ncopied=%v", got, want)
		}
	}
}

// Insert 失败（对象已 CopyObject 出去、asset 行没插成）——这条分支最容易漏：
// worker 里若先 return err 再记账，这个 dstKey 就永远不会进 GC。
func TestDuplicateRollbackCoversInsertFailure(t *testing.T) {
	const n = 12
	storage := newFakeCopyStorage()
	repo := &fakeAssetsRepo{insertFailOn: map[string]bool{}}
	gc := &fakeGC{}
	d := newTestDuplicator(storage, repo, gc)

	// 先跑一遍拿到 dstKey 的命名规律行不通（随机 hex），改为让**所有** Insert 都失败：
	// 那么每个成功 CopyObject 的对象都走「已复制但没入库」这条路。
	repo.insertAll = true

	_, err := d.Duplicate(context.Background(), 7, 42, "project_duplicate", snapshotWithKeys(n))
	if err == nil {
		t.Fatal("期望 Duplicate 返回错误")
	}
	want := storage.copiedKeys()
	got := gc.sorted()
	if len(want) == 0 {
		t.Fatal("测试无效：没有任何对象被复制")
	}
	if len(got) != len(want) {
		t.Fatalf("Insert 失败后漏回收: gc=%d copied=%d\ngc=%v\ncopied=%v",
			len(got), len(want), got, want)
	}
}

// 没有我方媒体的快照直接短路，不触发任何复制。
func TestDuplicateNoKeysShortCircuits(t *testing.T) {
	storage := newFakeCopyStorage()
	repo := &fakeAssetsRepo{insertFailOn: map[string]bool{}}
	gc := &fakeGC{}
	d := newTestDuplicator(storage, repo, gc)

	raw := json.RawMessage(`{"nodes":[{"id":"n1","content":"https://external.example/a.png"}]}`)
	res, err := d.Duplicate(context.Background(), 7, 42, "project_duplicate", raw)
	if err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	if len(storage.copiedKeys()) != 0 {
		t.Fatal("外部 URL 不该被复制")
	}
	if string(res.Snapshot) != string(raw) {
		t.Fatalf("快照应原样返回: %s", res.Snapshot)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
