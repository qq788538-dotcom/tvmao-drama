package generation

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

// 「画布节点丢 taskId」自愈对账的查询侧：按 (user, project, sink.nodeId) 找该节点
// 最新一次生成任务。前端节点卡在 generating 且本地 taskId 丢失时，靠它找回任务号
// 交还现有轮询——所以必须严格按 id 取最新，且不能越权命中别人的任务。

func insertCanvasTask(t *testing.T, repo *SQLiteRepository, userID, projectID int64, nodeID string) int64 {
	t.Helper()
	pid := projectID
	id, err := repo.Create(Task{
		UserID:    userID,
		ProjectID: &pid,
		ModelID:   "test-model",
		Status:    "queued",
		Input:     json.RawMessage(`{}`),
		Sink: json.RawMessage(fmt.Sprintf(
			`{"kind":"canvas_node","projectId":%d,"nodeId":%q}`, projectID, nodeID)),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return id
}

func TestLatestByCanvasNodePicksNewestForThatNode(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)

	insertCanvasTask(t, repo, 71, 710, "n-target")
	newest := insertCanvasTask(t, repo, 71, 710, "n-target")
	insertCanvasTask(t, repo, 71, 710, "n-other")

	got, err := repo.LatestByCanvasNode(71, 710, "n-target")
	if err != nil {
		t.Fatalf("LatestByCanvasNode: %v", err)
	}
	if got.ID != newest {
		t.Fatalf("应取该节点最新任务 id=%d, got=%d", newest, got.ID)
	}
}

func TestLatestByCanvasNodeScopedToOwner(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)

	insertCanvasTask(t, repo, 72, 720, "n-owned")

	if _, err := repo.LatestByCanvasNode(73, 720, "n-owned"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("他人任务不得命中, err=%v", err)
	}
}

func TestLatestByCanvasNodeIgnoresOtherSinkKinds(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)

	pid := int64(740)
	if _, err := repo.Create(Task{
		UserID:    74,
		ProjectID: &pid,
		ModelID:   "test-model",
		Status:    "queued",
		Input:     json.RawMessage(`{}`),
		Sink:      json.RawMessage(`{"kind":"drama_material","nodeId":"n-x"}`),
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if _, err := repo.LatestByCanvasNode(74, 740, "n-x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("非 canvas_node sink 不得命中, err=%v", err)
	}
}
