package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasgen"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
)

type fakeGenClient struct{ err error }

func (f fakeGenClient) Submit(context.Context, generation.SubmitInput) (*generation.SubmitResult, error) {
	return nil, f.err
}
func (f fakeGenClient) Get(context.Context, int64, int64) (*generation.Task, error) {
	return nil, nil
}
func (f fakeGenClient) LatestByCanvasNode(context.Context, int64, int64, string) (*generation.Task, error) {
	return nil, generation.ErrNotFound
}

func TestCanvasGenSubmitInputLimit400(t *testing.T) {
	msg := "该模型最多支持连接 1 个视频，当前连接了 2 个"
	h := NewCanvasGenHandler(
		canvasgen.NewService(fakeGenClient{err: &generation.InputLimitError{Msg: msg}}, nil),
		nil,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/canvas-tasks",
		strings.NewReader(`{"projectId":1,"nodeId":"n1","modelId":"m","inputs":{}}`))
	rec := httptest.NewRecorder()
	h.Submit(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("数量违约应 400（用户自解，不是网关故障）, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), msg) {
		t.Fatalf("响应应带原文案让前端直接透出: %s", rec.Body.String())
	}
}

// TestCanvasGenSubmitInputDuration400：Submit 返回 generation.InputDurationError
// （视频时长探测失败）→ 应 400 + 原文案透出，与 InputLimitError 同一模式
// （换视频/重传可自解，不是网关故障）。
func TestCanvasGenSubmitInputDuration400(t *testing.T) {
	msg := "无法读取输入视频时长，请确认视频有效后重试"
	h := NewCanvasGenHandler(
		canvasgen.NewService(fakeGenClient{err: &generation.InputDurationError{Msg: msg}}, nil),
		nil,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/canvas-tasks",
		strings.NewReader(`{"projectId":1,"nodeId":"n1","modelId":"m","inputs":{}}`))
	rec := httptest.NewRecorder()
	h.Submit(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), msg) {
		t.Fatalf("时长不可读应 400+原文案, got %d: %s", rec.Code, rec.Body.String())
	}
}
