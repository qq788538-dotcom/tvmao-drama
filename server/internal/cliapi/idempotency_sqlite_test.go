package cliapi

import (
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestSQLiteIdempotencyLifecycle(t *testing.T) {
	s := NewSQLiteIdempotency(dbtest.Open(t))

	rec, fresh, err := s.Begin(1, "create-node", "k1", "h1")
	if err != nil || !fresh || rec == nil {
		t.Fatalf("首次 Begin: rec=%v fresh=%v err=%v", rec, fresh, err)
	}
	// 进行中再来一次：不是新建，拿到 in_progress 记录
	rec, fresh, err = s.Begin(1, "create-node", "k1", "h1")
	if err != nil || fresh || rec == nil || rec.Status != IdempotencyInProgress {
		t.Fatalf("重复 Begin: rec=%v fresh=%v err=%v", rec, fresh, err)
	}
	if err := s.Finish(1, "create-node", "k1", 201, []byte(`{"id":"n1"}`)); err != nil {
		t.Fatal(err)
	}
	rec, fresh, err = s.Begin(1, "create-node", "k1", "h1")
	if err != nil || fresh || rec == nil || rec.StatusCode != 201 || string(rec.Body) != `{"id":"n1"}` {
		t.Fatalf("完成后重放: rec=%+v fresh=%v err=%v", rec, fresh, err)
	}
	// 不同 scope 同一个 key 互不影响
	if _, fresh, _ := s.Begin(1, "create-edge", "k1", "h1"); !fresh {
		t.Fatal("不同 scope 应视为新请求")
	}
}

func TestSQLiteIdempotencyAbortAllowsRetry(t *testing.T) {
	s := NewSQLiteIdempotency(dbtest.Open(t))
	if _, fresh, _ := s.Begin(1, "run", "k2", "h"); !fresh {
		t.Fatal("首次应为新建")
	}
	if err := s.Abort(1, "run", "k2"); err != nil {
		t.Fatal(err)
	}
	if _, fresh, _ := s.Begin(1, "run", "k2", "h"); !fresh {
		t.Fatal("Abort 后同 key 应能重新开始")
	}
}
