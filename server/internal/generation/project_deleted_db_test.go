package generation

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

// 项目删除后，在途任务走带退款的失败收口，产物不插资产行、不计配额（P9）。

func insertProject(t *testing.T, conn *sql.DB, uid int64, deleted bool) int64 {
	t.Helper()
	res, err := conn.Exec(`INSERT INTO projects (user_id, name) VALUES (?, 'p')`, uid)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if deleted {
		if _, err := conn.Exec(`UPDATE projects SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func countAssets(t *testing.T, conn *sql.DB, taskID int64) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM assets WHERE source_task_id = ?`, taskID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPersistOutputsSkipsAssetForDeletedProject(t *testing.T) {
	for _, tc := range []struct {
		name string
		hard bool
	}{{"软删", false}, {"已硬删", true}} {
		t.Run(tc.name, func(t *testing.T) {
			_, svc, marked := newAssetHarness(t, &flakyAssets{})
			conn := svc.db
			pid := insertProject(t, conn, 1, true)
			if tc.hard {
				if _, err := conn.Exec(`DELETE FROM projects WHERE id = ?`, pid); err != nil {
					t.Fatal(err)
				}
			}
			_, _, _, _, err := svc.persistOutputs(context.Background(), 1, 41, &pid, []string{"data:image/png;base64,aGk="}, 0)
			if !errors.Is(err, errProjectDeleted) {
				t.Fatalf("应报项目已删除, got %v", err)
			}
			if n := countAssets(t, conn, 41); n != 0 {
				t.Fatalf("不应插资产行, got %d", n)
			}
			if used := storageUsed(t, conn, 1); used != 0 {
				t.Fatalf("不应计配额, got %d", used)
			}
			if len(*marked) != 1 {
				t.Fatalf("已写入的对象应登记孤儿, got %v", *marked)
			}
		})
	}
}

func TestPersistOutputsLiveProjectRecordsAsset(t *testing.T) {
	_, svc, _ := newAssetHarness(t, &flakyAssets{})
	pid := insertProject(t, svc.db, 1, false)
	_, ids, _, _, err := svc.persistOutputs(context.Background(), 1, 42, &pid, []string{"data:image/png;base64,aGk="}, 0)
	if err != nil || ids[0] == 0 {
		t.Fatalf("未删项目照常登记: ids=%v err=%v", ids, err)
	}
}

// 同步出图时项目已被删：任务失败退款，原因写明项目已删除。
func TestFinalizeFailsWithRefundWhenProjectDeleted(t *testing.T) {
	_, svc, _ := newAssetHarness(t, &flakyAssets{})
	conn := svc.db
	svc.concurrency = NewConcurrencyRegistry()
	pid := insertProject(t, conn, 1, true)

	png := base64.StdEncoding.EncodeToString(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...))
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + png + `"}]}`))
	}))
	t.Cleanup(up.Close)

	id := insertDirectRunning(t, conn, 1, "m-pd")
	model := &models.ModelConfig{ModelID: "m-pd", BasePath: "/gen"}
	if _, err := svc.callAndComplete(context.Background(), id, 1, model, "k", up.URL, []byte(`{}`), 1, &pid, 40, nil, nil, nil, ""); !errors.Is(err, errProjectDeleted) {
		t.Fatalf("err=%v", err)
	}
	status, msg := taskStatusErr(t, conn, id)
	if status != "failed" || msg != projectDeletedReason {
		t.Fatalf("status=%s msg=%s", status, msg)
	}
	assertOps(t, svc, "RefundTx")
	if n := countAssets(t, conn, id); n != 0 {
		t.Fatalf("不应插资产行, got %d", n)
	}
}

func TestSweepDeletedProjectTasks(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	dead := insertProject(t, conn, uid, true)
	live := insertProject(t, conn, uid, false)
	insert := func(status string, pid any) int64 {
		t.Helper()
		res, err := conn.Exec(`INSERT INTO generation_tasks (user_id, project_id, model_id, status, input, frozen_cents, sink, lease_version)
			VALUES (?, ?, 'm', ?, '{}', 40, '{"kind":"none"}', 2)`, uid, pid, status)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	queued := insert("queued", dead)
	processing := insert("processing", dead)
	retrying := insert("retry_pending", dead)
	gone := insert("running", int64(999999)) // 项目行已被硬删
	done := insert("succeeded", dead)
	liveQueued := insert("queued", live)
	noProject := insert("queued", nil)

	svc.SweepDeletedProjectTasks()

	for _, id := range []int64{queued, processing, retrying, gone} {
		status, lease := taskState(t, conn, id)
		if status != "failed" || lease != 3 {
			t.Fatalf("task=%d 应失败收口并推进代次: status=%s lease=%d", id, status, lease)
		}
		if _, msg := taskStatusErr(t, conn, id); msg != projectDeletedReason {
			t.Fatalf("task=%d msg=%s", id, msg)
		}
	}
	for id, want := range map[int64]string{done: "succeeded", liveQueued: "queued", noProject: "queued"} {
		if status, lease := taskState(t, conn, id); status != want || lease != 2 {
			t.Fatalf("task=%d 不应被动: status=%s lease=%d", id, status, lease)
		}
	}
	// 每个被收口的任务恰好一次退款，绝不是裸 UPDATE。
	assertOps(t, svc, "RefundTx", "RefundTx", "RefundTx", "RefundTx")

	// 再扫一遍幂等。
	svc.SweepDeletedProjectTasks()
	assertOps(t, svc, "RefundTx", "RefundTx", "RefundTx", "RefundTx")
}
