package generation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeRepersistStore struct {
	tasks    []Task
	updated  map[int64]json.RawMessage
	kinds    map[int64]string
	attempts map[int64]int
	nextAt   map[int64]time.Time
}

func newFakeRepersistStore(tasks ...Task) *fakeRepersistStore {
	return &fakeRepersistStore{
		tasks:    tasks,
		updated:  map[int64]json.RawMessage{},
		kinds:    map[int64]string{},
		attempts: map[int64]int{},
		nextAt:   map[int64]time.Time{},
	}
}

func (f *fakeRepersistStore) ListExternalOutputs(int, int, int, time.Time) ([]Task, error) {
	return f.tasks, nil
}
func (f *fakeRepersistStore) UpdateOutput(id int64, out json.RawMessage, kind string) error {
	f.updated[id] = out
	f.kinds[id] = kind
	return nil
}
func (f *fakeRepersistStore) MarkRepersistAttempt(id int64, n int, at time.Time) error {
	f.attempts[id] = n
	f.nextAt[id] = at
	return nil
}

type recordingRewriter struct{ calls [][3]string }

func (r *recordingRewriter) RewriteMediaRef(_ context.Context, userID int64, oldURL, newURL string) error {
	r.calls = append(r.calls, [3]string{jsonNum(userID), oldURL, newURL})
	return nil
}

func jsonNum(v int64) string { b, _ := json.Marshal(v); return string(b) }

func TestRepersistDelayDoublesAndTenAttemptsSpanAboutTwoDays(t *testing.T) {
	want := []time.Duration{5, 10, 20, 40, 80}
	for i, m := range want {
		if got := repersistDelay(i + 1); got != m*time.Minute {
			t.Fatalf("attempt %d: got %s want %s", i+1, got, m*time.Minute)
		}
	}
	var total time.Duration
	for i := 1; i < RepersistMaxAttempts; i++ {
		total += repersistDelay(i)
	}
	// 5m·(2^9-1) ≈ 42.6h:上游保留期约两周,两天内翻倍重试足够覆盖「整晚很慢」,
	// 又不会对早已清掉的文件永远打 404。
	if total < 40*time.Hour || total > 48*time.Hour {
		t.Fatalf("10 次总窗口 %s 不在 40h~48h", total)
	}
}

func TestRepersistOnceMovesExternalOutputAndRewritesCopies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNGDATA"))
	}))
	defer srv.Close()
	old := srv.URL + "/chatgpt2api_docker/1.png"
	pid := int64(133)
	task := Task{ID: 7, UserID: 5, ProjectID: &pid, Status: "succeeded",
		Output: json.RawMessage(`{"outputUrls":["` + old + `"],"assetIds":[0],"upstreamUrls":["` + old + `"],"persistAttempts":2,"persistNextAt":"2026-01-01T00:00:00Z"}`)}
	store := newFakeRepersistStore(task)
	rw := &recordingRewriter{}
	svc := &Service{storage: &memStorage{}, httpClient: srv.Client()}

	fixed, gaveUp, failed := svc.RepersistOnce(context.Background(), store, rw)
	if fixed != 1 || gaveUp != 0 || failed != 0 {
		t.Fatalf("got fixed=%d gaveUp=%d failed=%d", fixed, gaveUp, failed)
	}
	var out map[string]any
	if err := json.Unmarshal(store.updated[7], &out); err != nil {
		t.Fatal(err)
	}
	urls := anyStrings(out["outputUrls"])
	// 与 persistOutputs 首搬同一个确定性 key:gen/task-{id}-{i}。
	if len(urls) != 1 || urls[0] != "media://static/gen/task-7-0" {
		t.Fatalf("outputUrls=%v", urls)
	}
	if out["outputKind"] != "image" || store.kinds[7] != "image" {
		t.Fatalf("outputKind 未按 mime 补齐: %v / %q", out["outputKind"], store.kinds[7])
	}
	if _, ok := out["persistAttempts"]; ok {
		t.Fatalf("修好后不应残留重试簿记: %v", out)
	}
	if _, ok := out["persistNextAt"]; ok {
		t.Fatalf("修好后不应残留重试簿记: %v", out)
	}
	if got := anyStrings(out["upstreamUrls"]); len(got) != 1 || got[0] != old {
		t.Fatalf("upstreamUrls 是故意存的上游原始地址,不该被动: %v", got)
	}
	if len(rw.calls) != 1 || rw.calls[0] != [3]string{"5", old, "media://static/gen/task-7-0"} {
		t.Fatalf("副本改写调用不对: %v", rw.calls)
	}
	if len(store.attempts) != 0 {
		t.Fatalf("成功不该记重试: %v", store.attempts)
	}
}

func TestRepersistOnceFailureCountsAttemptAndBacksOff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	old := srv.URL + "/gone.png"
	mk := func(id int64, attempts int) Task {
		return Task{ID: id, UserID: 5, Status: "succeeded",
			Output: json.RawMessage(`{"outputUrls":["` + old + `"],"assetIds":[0],"persistAttempts":` + jsonNum(int64(attempts)) + `}`)}
	}
	store := newFakeRepersistStore(mk(1, 0), mk(2, RepersistMaxAttempts-1))
	rw := &recordingRewriter{}
	svc := &Service{storage: &memStorage{}, httpClient: srv.Client()}

	before := time.Now()
	fixed, gaveUp, failed := svc.RepersistOnce(context.Background(), store, rw)
	if fixed != 0 || gaveUp != 1 || failed != 1 {
		t.Fatalf("got fixed=%d gaveUp=%d failed=%d", fixed, gaveUp, failed)
	}
	// 404 也算一次失败(上游有「先给 URL、文件稍后才就绪」的假 404),不单独打死标。
	if store.attempts[1] != 1 {
		t.Fatalf("task1 attempts=%d want 1", store.attempts[1])
	}
	if d := store.nextAt[1].Sub(before); d < 5*time.Minute || d > 6*time.Minute {
		t.Fatalf("task1 第 1 次失败后应 5 分钟再试,got %s", d)
	}
	if store.attempts[2] != RepersistMaxAttempts {
		t.Fatalf("task2 attempts=%d want %d(放弃)", store.attempts[2], RepersistMaxAttempts)
	}
	if len(store.updated) != 0 || len(rw.calls) != 0 {
		t.Fatalf("失败不该回写: %v %v", store.updated, rw.calls)
	}
}

func TestRepersistOnceSkipsWhenNothingExternal(t *testing.T) {
	// 防御:ListExternalOutputs 的 SQL 与这里的 http 前缀判断若不一致,会出现「列出来
	// 却没槽位可搬」——按失败计一次,而不是静默成功后永远重复选中。
	task := Task{ID: 9, UserID: 5, Status: "succeeded",
		Output: json.RawMessage(`{"outputUrls":["media://static/gen/task-9-0"],"assetIds":[1]}`)}
	store := newFakeRepersistStore(task)
	svc := &Service{storage: &memStorage{}, httpClient: http.DefaultClient}
	fixed, _, failed := svc.RepersistOnce(context.Background(), store)
	if fixed != 0 || failed != 1 || store.attempts[9] != 1 {
		t.Fatalf("fixed=%d failed=%d attempts=%v", fixed, failed, store.attempts)
	}
	if len(store.updated) != 0 {
		t.Fatalf("不该回写: %v", store.updated)
	}
}
