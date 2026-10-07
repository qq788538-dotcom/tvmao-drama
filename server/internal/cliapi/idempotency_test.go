package cliapi

import (
	"errors"
	"testing"
)

// memIdempotency 是 IdempotencyStore 的内存实现，语义与 MySQL 版一致。
type memIdempotency struct {
	rows map[string]*IdempotencyRecord
}

func newMemIdempotency() *memIdempotency {
	return &memIdempotency{rows: map[string]*IdempotencyRecord{}}
}

func (m *memIdempotency) key(userID int64, scope, k string) string {
	return string(rune(userID)) + "|" + scope + "|" + k
}

func (m *memIdempotency) Begin(userID int64, scope, k, hash string) (*IdempotencyRecord, bool, error) {
	kk := m.key(userID, scope, k)
	if rec, ok := m.rows[kk]; ok {
		return rec, false, nil
	}
	rec := &IdempotencyRecord{Status: IdempotencyInProgress, RequestHash: hash}
	m.rows[kk] = rec
	return rec, true, nil
}

func (m *memIdempotency) Finish(userID int64, scope, k string, statusCode int, body []byte) error {
	kk := m.key(userID, scope, k)
	rec, ok := m.rows[kk]
	if !ok {
		return errors.New("no record")
	}
	rec.Status = IdempotencyDone
	rec.StatusCode = statusCode
	rec.Body = body
	return nil
}

func (m *memIdempotency) Abort(userID int64, scope, k string) error {
	delete(m.rows, m.key(userID, scope, k))
	return nil
}

func TestHashRequestIsStableAcrossKeyOrder(t *testing.T) {
	// Go 的 map 序列化按 key 排序，天然 canonical——但要有测试守住这个假设，
	// 否则将来换 encoder 会静默让「同请求」变成「不同请求」。
	a := HashRequest(map[string]any{"a": 1, "b": 2})
	b := HashRequest(map[string]any{"b": 2, "a": 1})
	if a != b || a == "" {
		t.Fatalf("hash 不稳定: %q vs %q", a, b)
	}
	if HashRequest(map[string]any{"a": 1}) == a {
		t.Fatal("不同请求不应同 hash")
	}
}

// 无 key = 不去重，fn 每次都跑。CLI 的读命令与不带 key 的写都走这条路。
func TestRunIdempotentWithoutKeyAlwaysExecutes(t *testing.T) {
	store := newMemIdempotency()
	calls := 0
	fn := func() (int, any, error) { calls++; return 201, map[string]any{"n": calls}, nil }

	for i := 0; i < 3; i++ {
		if _, _, err := RunIdempotent(store, 1, "node_create", "", nil, fn); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRunIdempotentReplaysSameRequest(t *testing.T) {
	store := newMemIdempotency()
	req := map[string]any{"type": "image-generator"}
	calls := 0
	fn := func() (int, any, error) { calls++; return 201, map[string]any{"id": "n-1"}, nil }

	status1, body1, err := RunIdempotent(store, 1, "node_create", "k1", req, fn)
	if err != nil {
		t.Fatal(err)
	}
	status2, body2, err := RunIdempotent(store, 1, "node_create", "k1", req, fn)
	if err != nil {
		t.Fatal(err)
	}

	if calls != 1 {
		t.Fatalf("重放必须不再执行业务，calls = %d", calls)
	}
	if status1 != status2 || string(body1) != string(body2) {
		t.Fatalf("重放响应不一致: %d %s vs %d %s", status1, body1, status2, body2)
	}
}

func TestRunIdempotentDifferentRequestConflicts(t *testing.T) {
	store := newMemIdempotency()
	fn := func() (int, any, error) { return 201, map[string]any{}, nil }

	if _, _, err := RunIdempotent(store, 1, "node_create", "k1", map[string]any{"a": 1}, fn); err != nil {
		t.Fatal(err)
	}
	_, _, err := RunIdempotent(store, 1, "node_create", "k1", map[string]any{"a": 2}, fn)

	var ce *Error
	if !errors.As(err, &ce) || ce.Code != CodeIdempotencyKeyReused {
		t.Fatalf("同 key 不同请求应报 IDEMPOTENCY_KEY_REUSED, got %v", err)
	}
}

func TestRunIdempotentInProgressConflicts(t *testing.T) {
	store := newMemIdempotency()
	req := map[string]any{"a": 1}
	// 手动留一条 in_progress，模拟首次请求还没跑完时的重放。
	_, _, _ = store.Begin(1, "node_run", "k1", HashRequest(req))

	_, _, err := RunIdempotent(store, 1, "node_run", "k1", req, func() (int, any, error) {
		t.Fatal("处理中不得再次执行业务")
		return 0, nil, nil
	})
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != CodeIdempotencyInProgress {
		t.Fatalf("got %v", err)
	}
}

// 业务失败时必须删掉 in_progress 记录，否则同一个 key 会被永久占住，
// 用户重试只会一直撞 IDEMPOTENCY_IN_PROGRESS。
func TestRunIdempotentAbortsOnBusinessError(t *testing.T) {
	store := newMemIdempotency()
	req := map[string]any{"a": 1}
	boom := New(CodeUpstreamUnavailable, "上游炸了")

	_, _, err := RunIdempotent(store, 1, "node_run", "k1", req, func() (int, any, error) {
		return 0, nil, boom
	})
	if !errors.Is(err, error(boom)) {
		t.Fatalf("应原样返回业务错误, got %v", err)
	}

	calls := 0
	if _, _, err := RunIdempotent(store, 1, "node_run", "k1", req, func() (int, any, error) {
		calls++
		return 201, map[string]any{}, nil
	}); err != nil {
		t.Fatalf("失败后重试应能重新执行, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

// 幂等按 (user, scope, key) 隔离：不同用户/不同操作用同一个 key 互不干扰。
func TestRunIdempotentScopedByUserAndScope(t *testing.T) {
	store := newMemIdempotency()
	req := map[string]any{"a": 1}
	calls := 0
	fn := func() (int, any, error) { calls++; return 201, map[string]any{}, nil }

	_, _, _ = RunIdempotent(store, 1, "node_create", "same", req, fn)
	_, _, _ = RunIdempotent(store, 2, "node_create", "same", req, fn)
	_, _, _ = RunIdempotent(store, 1, "edge_create", "same", req, fn)

	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}
