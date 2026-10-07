package main

import (
	"context"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
	"github.com/james/huabu_studio/apps/api/internal/generation"
)

func allAuth(auths []string, want string) bool {
	if len(auths) == 0 {
		return false
	}
	for _, a := range auths {
		if a != want {
			return false
		}
	}
	return true
}

// 每模型独立上游，画布生成（队列执行路径）：配了独立上游的模型打到自己的地址、带自己的 Key；
// 没配的模型仍走全局。
func TestModelUpstreamCanvasGeneration(t *testing.T) {
	h := newHarness(t, "n1", "n2")
	own := newFakeUpstream(t)
	h.exec(`UPDATE model_configs SET upstream_base_url = ?, upstream_api_key = 'sk-own-model' WHERE model_id = 'img-test'`, own.srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.w.Gen.StartBackground(ctx)

	if _, err := h.w.CanvasGen.Enqueue(ctx, canvasgen.EnqueueInput{UserID: 1, ProjectID: h.pid, NodeID: "n1", ModelID: "img-test",
		Inputs: map[string]any{"prompt": "猫"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "n1 成功", func() bool { n := h.node(t, "n1"); return n != nil && n["status"] == "succeeded" })
	if own.imagePosts.Load() != 1 || h.up.imagePosts.Load() != 0 {
		t.Fatalf("img-test 应只打到独立上游: own=%d global=%d", own.imagePosts.Load(), h.up.imagePosts.Load())
	}
	if !allAuth(own.authHeaders(), "Bearer sk-own-model") {
		t.Fatalf("独立上游应收到模型自己的 Key, got %v", own.authHeaders())
	}

	// 清空后回落全局。
	h.exec(`UPDATE model_configs SET upstream_base_url = '', upstream_api_key = '' WHERE model_id = 'img-test'`)
	if _, err := h.w.CanvasGen.Enqueue(ctx, canvasgen.EnqueueInput{UserID: 1, ProjectID: h.pid, NodeID: "n2", ModelID: "img-test",
		Inputs: map[string]any{"prompt": "狗"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "n2 成功", func() bool { n := h.node(t, "n2"); return n != nil && n["status"] == "succeeded" })
	if h.up.imagePosts.Load() != 1 || own.imagePosts.Load() != 1 {
		t.Fatalf("清空后应走全局: own=%d global=%d", own.imagePosts.Load(), h.up.imagePosts.Load())
	}
	if !allAuth(h.up.authHeaders(), "Bearer sk-test") {
		t.Fatalf("全局上游应收到全局 Key, got %v", h.up.authHeaders())
	}
}

// 重启续轮询（ResumeOrphanedTasks）：已拿到上游任务号的任务按模型的独立上游续轮询；
// 全局 base_domain 为空也不影响配了独立上游的模型。
func TestModelUpstreamResumePolling(t *testing.T) {
	h := newHarness(t, "n3")
	own := newFakeUpstream(t)
	h.exec(`UPDATE model_configs SET upstream_base_url = ?, upstream_api_key = 'sk-own-vid' WHERE model_id = 'vid-test'`, own.srv.URL)
	h.exec(`UPDATE settings SET value = '' WHERE "key" = 'base_domain'`)
	sink := `{"kind":"canvas_node","projectId":` + itoa(h.pid) + `,"nodeId":"n3","userId":1}`
	h.exec(`INSERT INTO generation_tasks (user_id, project_id, model_id, status, input, sink, upstream_task_id, upstream_called_at, owner_instance, heartbeat_at, lease_version)
		VALUES (1, ?, 'vid-test', 'processing', '{"modelId":"vid-test","inputs":{"prompt":"y"},"note":""}', ?, 'up-1', CURRENT_TIMESTAMP, 'old-proc', CURRENT_TIMESTAMP, 1)`,
		h.pid, sink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.w.Gen.StartBackground(ctx)
	waitFor(t, "n3 成功", func() bool { n := h.node(t, "n3"); return n != nil && n["status"] == "succeeded" })
	if own.videoPolls.Load() == 0 || h.up.videoPolls.Load() != 0 {
		t.Fatalf("应只向独立上游续轮询: own=%d global=%d", own.videoPolls.Load(), h.up.videoPolls.Load())
	}
	if !allAuth(own.authHeaders(), "Bearer sk-own-vid") {
		t.Fatalf("续轮询应带模型自己的 Key, got %v", own.authHeaders())
	}
}

// 直连提交（无 sink，Agent 工具走这条）：同样按模型解析上游。
func TestModelUpstreamDirectSubmit(t *testing.T) {
	h := newHarness(t)
	own := newFakeUpstream(t)
	h.exec(`UPDATE model_configs SET upstream_base_url = ?, upstream_api_key = 'sk-own-direct' WHERE model_id = 'img-test'`, own.srv.URL)
	ctx := context.Background()
	res, err := h.w.Gen.Submit(ctx, generation.SubmitInput{UserID: 1, ModelID: "img-test", Inputs: map[string]any{"prompt": "猫"}})
	if err != nil || res.Status != "succeeded" {
		t.Fatalf("submit: %+v %v", res, err)
	}
	if own.imagePosts.Load() != 1 || h.up.imagePosts.Load() != 0 || !allAuth(own.authHeaders(), "Bearer sk-own-direct") {
		t.Fatalf("应打到独立上游: own=%d global=%d auth=%v", own.imagePosts.Load(), h.up.imagePosts.Load(), own.authHeaders())
	}
	h.exec(`UPDATE model_configs SET upstream_base_url = '', upstream_api_key = '' WHERE model_id = 'img-test'`)
	if res, err := h.w.Gen.Submit(ctx, generation.SubmitInput{UserID: 1, ModelID: "img-test", Inputs: map[string]any{"prompt": "狗"}}); err != nil || res.Status != "succeeded" {
		t.Fatalf("submit: %+v %v", res, err)
	}
	if h.up.imagePosts.Load() != 1 || !allAuth(h.up.authHeaders(), "Bearer sk-test") {
		t.Fatalf("未配置时应走全局: global=%d auth=%v", h.up.imagePosts.Load(), h.up.authHeaders())
	}
}
