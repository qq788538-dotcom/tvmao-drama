package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasgen"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
)

// GET /canvas-tasks/by-node——「节点丢 taskId」自愈对账的 HTTP 面。
// 关键契约:查无是 404(前端连续查无才判死节点,必须与 5xx 区分);
// 成功时带 taskId 让前端续接现有轮询。

type fakeNodeGenClient struct {
	fakeGenClient
	task *generation.Task
	err  error
}

func (f fakeNodeGenClient) LatestByCanvasNode(context.Context, int64, int64, string) (*generation.Task, error) {
	return f.task, f.err
}

func TestCanvasGenNodeStatusReturnsTask(t *testing.T) {
	h := NewCanvasGenHandler(
		canvasgen.NewService(fakeNodeGenClient{task: &generation.Task{
			ID:     833,
			Status: "succeeded",
			Output: json.RawMessage(`{"outputUrls":["media://u5/gen_task-833-0"],"assetIds":[795]}`),
		}}, nil),
		nil,
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/canvas-tasks/by-node?projectId=44&nodeId=n-rWYvATy8", nil)
	rec := httptest.NewRecorder()
	h.NodeStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		TaskID int64  `json:"taskId"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TaskID != 833 || body.Status != "succeeded" {
		t.Fatalf("应带 taskId=833 succeeded, got %s", rec.Body.String())
	}
}

func TestCanvasGenNodeStatusNotFound404(t *testing.T) {
	h := NewCanvasGenHandler(
		canvasgen.NewService(fakeNodeGenClient{err: generation.ErrNotFound}, nil),
		nil,
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/canvas-tasks/by-node?projectId=44&nodeId=n-gone", nil)
	rec := httptest.NewRecorder()
	h.NodeStatus(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("查无应 404(前端有效信号,不能混入 5xx), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCanvasGenNodeStatusMissingParams400(t *testing.T) {
	h := NewCanvasGenHandler(canvasgen.NewService(fakeNodeGenClient{}, nil), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/canvas-tasks/by-node?nodeId=n-x", nil)
	rec := httptest.NewRecorder()
	h.NodeStatus(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 projectId 应 400, got %d", rec.Code)
	}
}
