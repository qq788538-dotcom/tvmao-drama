package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func zeroFinishRetryDelays(t *testing.T) {
	t.Helper()
	old := finishRetryDelays
	finishRetryDelays = []time.Duration{0, 0, 0}
	t.Cleanup(func() { finishRetryDelays = old })
}

// 执行 ctx 已取消（关闭/队列超时）时，成功收口仍要落库——上游钱已花、产物已在盘上。
func TestFinishSuccessDurableIgnoresCanceledCtx(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	res, _ := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, lease_version)
		VALUES (?, 'm-f', 'running', '{}', 40, '{"kind":"canvas_node","projectId":1,"nodeId":"n"}', 3)`, uid)
	id, _ := res.LastInsertId()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, _ := json.Marshal(map[string]any{"outputUrls": []string{"media://k"}})
	won, err := svc.finishSuccessDurable(ctx, id, 3, finishOutcome{output: out, actualCredits: 9,
		sinkRes: SinkResult{OutputURLs: []string{"media://k"}}})
	if err != nil || !won {
		t.Fatalf("取消的执行 ctx 不应让收口失败, won=%v err=%v", won, err)
	}
	if status, _ := taskState(t, conn, id); status != "succeeded" {
		t.Fatalf("应 succeeded, got %s", status)
	}
}

func TestFinishSuccessDurableRetriesBusy(t *testing.T) {
	zeroFinishRetryDelays(t)
	svc := &Service{}
	calls := 0
	svc.finishSuccessHook = func(ctx context.Context, _, _ int64, _ finishOutcome) (bool, error) {
		calls++
		if ctx.Err() != nil {
			t.Fatalf("每次尝试应拿到未取消的独立 ctx")
		}
		if calls < 3 {
			return false, fmt.Errorf("finish: %w", errors.New("database is locked (5) (SQLITE_BUSY)"))
		}
		return true, nil
	}
	won, err := svc.finishSuccessDurable(context.Background(), 1, 1, finishOutcome{})
	if err != nil || !won || calls != 3 {
		t.Fatalf("BUSY 应重试到成功, won=%v err=%v calls=%d", won, err, calls)
	}
}

func TestFinishSuccessDurableBoundedAndNoRetryOnPermanent(t *testing.T) {
	zeroFinishRetryDelays(t)
	svc := &Service{}
	calls := 0
	svc.finishSuccessHook = func(context.Context, int64, int64, finishOutcome) (bool, error) {
		calls++
		return false, errors.New("database is locked (5) (SQLITE_BUSY)")
	}
	if _, err := svc.finishSuccessDurable(context.Background(), 1, 1, finishOutcome{}); err == nil {
		t.Fatal("一直 BUSY 最终应返回错误")
	}
	if calls != 1+len(finishRetryDelays) {
		t.Fatalf("重试必须有界, calls=%d", calls)
	}

	calls = 0
	svc.finishSuccessHook = func(context.Context, int64, int64, finishOutcome) (bool, error) {
		calls++
		return false, errors.New("constraint failed")
	}
	if _, err := svc.finishSuccessDurable(context.Background(), 1, 1, finishOutcome{}); err == nil || calls != 1 {
		t.Fatalf("非瞬时错误不重试, calls=%d err=%v", calls, err)
	}

	// 失租（won=false, err=nil）不重试。
	calls = 0
	svc.finishSuccessHook = func(context.Context, int64, int64, finishOutcome) (bool, error) {
		calls++
		return false, nil
	}
	if won, err := svc.finishSuccessDurable(context.Background(), 1, 1, finishOutcome{}); won || err != nil || calls != 1 {
		t.Fatalf("失租不重试, calls=%d", calls)
	}
}

// ncruces 下语句执行中途超时返回 sqlite3: interrupted（不带 DeadlineExceeded），必须认成瞬时错误。
func TestIsTransientDBErrorRecognizesInterrupt(t *testing.T) {
	conn := dbtest.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var n int64
	err := conn.QueryRowContext(ctx, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c)
		SELECT count(*) FROM c`).Scan(&n)
	if err == nil {
		t.Fatal("无限递归查询应被超时打断")
	}
	if !isTransientDBError(err) {
		t.Fatalf("语句中途超时应判为瞬时错误: %v (deadline=%v interrupt=%v)",
			err, errors.Is(err, context.DeadlineExceeded), errors.Is(err, sqlite3.INTERRUPT))
	}

	for _, c := range []struct {
		err  error
		want bool
	}{
		{sqlite3.INTERRUPT, true},
		{fmt.Errorf("finish: %w", sqlite3.INTERRUPT), true},
		{fmt.Errorf("finish: %w", sqlite3.BUSY), true},
		{sqlite3.LOCKED, true},
		{fmt.Errorf("finish: %w", context.DeadlineExceeded), true},
		{fmt.Errorf("finish: %w", context.Canceled), false},
		{errors.Join(sqlite3.INTERRUPT, context.Canceled), false},
		{sqlite3.CONSTRAINT, false},
		{errors.New("constraint failed"), false},
	} {
		if got := isTransientDBError(c.err); got != c.want {
			t.Errorf("isTransientDBError(%v) = %v, 期望 %v", c.err, got, c.want)
		}
	}
}

func TestFinishSuccessDurableRetriesInterrupt(t *testing.T) {
	zeroFinishRetryDelays(t)
	svc := &Service{}
	calls := 0
	svc.finishSuccessHook = func(context.Context, int64, int64, finishOutcome) (bool, error) {
		calls++
		if calls < 3 {
			return false, fmt.Errorf("finish: %w", sqlite3.INTERRUPT)
		}
		return true, nil
	}
	won, err := svc.finishSuccessDurable(context.Background(), 1, 1, finishOutcome{})
	if err != nil || !won || calls != 3 {
		t.Fatalf("单次超时被中断应重试到成功, won=%v err=%v calls=%d", won, err, calls)
	}

	// 执行 ctx 只是到期（30 分钟队列超时）：照常重试。
	calls = 0
	dctx, dcancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer dcancel()
	if won, err := svc.finishSuccessDurable(dctx, 1, 1, finishOutcome{}); err != nil || !won || calls != 3 {
		t.Fatalf("执行 ctx 到期不应影响重试, won=%v err=%v calls=%d", won, err, calls)
	}
}

// 执行 ctx 被主动取消（关闭流程）：做完手头这一次就返回，不再退避重试。
func TestFinishSuccessDurableNoRetryWhenParentCanceled(t *testing.T) {
	zeroFinishRetryDelays(t)
	svc := &Service{}
	calls := 0
	svc.finishSuccessHook = func(ctx context.Context, _, _ int64, _ finishOutcome) (bool, error) {
		calls++
		if ctx.Err() != nil {
			t.Fatalf("单次尝试的 ctx 不应继承取消")
		}
		return false, fmt.Errorf("finish: %w", sqlite3.INTERRUPT)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.finishSuccessDurable(ctx, 1, 1, finishOutcome{}); err == nil || calls != 1 {
		t.Fatalf("主动取消后不应重试, calls=%d err=%v", calls, err)
	}
}
