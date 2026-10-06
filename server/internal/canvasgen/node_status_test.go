package canvasgen

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/generation"
)

// 「节点丢 taskId」自愈对账的 service 侧：按 nodeId 找回最新任务并给出
// 与 GetStatus 同形状的状态 + taskId。前端拿到 taskId 后交还现有轮询,
// 所以这里必须把 taskId 一起带回,光有状态不够。

type fakeNodeGen struct {
	genClient
	task *generation.Task
	err  error
}

func (f *fakeNodeGen) LatestByCanvasNode(_ context.Context, _, _ int64, _ string) (*generation.Task, error) {
	return f.task, f.err
}

func TestGetNodeStatusReturnsTaskIDWithStatus(t *testing.T) {
	svc := NewService(&fakeNodeGen{task: &generation.Task{
		ID:     833,
		Status: "succeeded",
		Output: json.RawMessage(`{"outputUrls":["media://u5/gen_task-833-0"],"assetIds":[795]}`),
	}}, nil)

	st, err := svc.GetNodeStatus(context.Background(), 5, 44, "n-rWYvATy8")
	if err != nil {
		t.Fatalf("GetNodeStatus: %v", err)
	}
	if st.TaskID != 833 || st.Status.Status != "succeeded" {
		t.Fatalf("应带回 taskId=833 succeeded, got=%+v", st)
	}
	if len(st.OutputURLs) != 1 || st.OutputURLs[0] != "media://u5/gen_task-833-0" {
		t.Fatalf("outputUrls 应透传, got=%v", st.OutputURLs)
	}
	if len(st.AssetIDs) != 1 || st.AssetIDs[0] != 795 {
		t.Fatalf("assetIds 应透传, got=%v", st.AssetIDs)
	}
}

func TestGetNodeStatusMapsQueuedToProcessing(t *testing.T) {
	svc := NewService(&fakeNodeGen{task: &generation.Task{ID: 9, Status: "queued"}}, nil)

	st, err := svc.GetNodeStatus(context.Background(), 5, 44, "n-x")
	if err != nil {
		t.Fatalf("GetNodeStatus: %v", err)
	}
	if st.TaskID != 9 || st.Status.Status != "processing" {
		t.Fatalf("排队中应映射 processing 并带 taskId, got=%+v", st)
	}
}

func TestGetNodeStatusPropagatesNotFound(t *testing.T) {
	svc := NewService(&fakeNodeGen{err: generation.ErrNotFound}, nil)

	if _, err := svc.GetNodeStatus(context.Background(), 5, 44, "n-x"); !errors.Is(err, generation.ErrNotFound) {
		t.Fatalf("无任务应透传 ErrNotFound 供 handler 映射 404, err=%v", err)
	}
}
