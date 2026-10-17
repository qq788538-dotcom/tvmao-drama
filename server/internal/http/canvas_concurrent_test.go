package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/canvasowner"
	"github.com/james/huabu_studio/apps/api/internal/canvasstate"
	"github.com/james/huabu_studio/apps/api/internal/projects"
)

func teamCanvasRequest(t *testing.T, method, path string, body []byte, params map[string]string, actor int64) *http.Request {
	t.Helper()
	req := newProjectCanvasRequest(t, method, path, body, params)
	return req.WithContext(withActorUserID(req.Context(), actor))
}

// 核心分流：团队身份（actor>0）写画布**不过编辑锁**。锁值就算是空的、过期的、
// 别人的，也照写不误——服务端的串行化由 MutateClientSnapshot 的 SELECT…FOR UPDATE
// 行锁承担。这条如果回归，多人同时编辑会立刻退化回「一个人写、其他人被 409 顶住」。
func TestCanvasMutationTeamIdentitySkipsOwnerLock(t *testing.T) {
	repo := &fakeProjectRepository{
		project:       &projects.Project{ID: 42, UserID: 7},
		mutatePayload: json.RawMessage(`{"nodes":[{"id":"n1","type":"text"}],"connections":[]}`),
		mutateVersion: 5,
	}
	// Require 会返回「锁被别人抢走了」——团队路径必须根本不调它。
	owner := &fakeCanvasOwner{requireErr: canvasowner.ErrOwnerLost}
	handler := NewCanvasMutationHandler(repo, owner, nil)
	rec := httptest.NewRecorder()

	handler.PatchNode(rec, teamCanvasRequest(
		t, http.MethodPatch, "/api/v1/projects/42/canvas/nodes/n1",
		[]byte(`{"ownerToken":"team:deadbeef","patch":{"settings":{"prompt":"new"}}}`),
		map[string]string{"nodeID": "n1"}, 9,
	))

	if rec.Code != http.StatusOK {
		t.Fatalf("团队写入 status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if owner.requiredToken != "" {
		t.Fatalf("团队路径调了 owner.Require(token=%q)——去锁没生效", owner.requiredToken)
	}
	if !repo.mutateCalled {
		t.Fatal("团队写入没落到 MutateClientSnapshot")
	}
}

// 个人身份（actor==0）逐字保持旧版行为：锁校验必须仍然生效，失锁就是 409。
// 这条是「个人路径零回归」承诺的钉子。
func TestCanvasMutationPersonalIdentityStillRequiresOwnerLock(t *testing.T) {
	repo := &fakeProjectRepository{
		project:       &projects.Project{ID: 42, UserID: 7},
		mutatePayload: json.RawMessage(`{"nodes":[{"id":"n1","type":"text"}],"connections":[]}`),
	}
	owner := &fakeCanvasOwner{requireErr: canvasowner.ErrOwnerLost}
	handler := NewCanvasMutationHandler(repo, owner, nil)
	rec := httptest.NewRecorder()

	handler.PatchNode(rec, newProjectCanvasRequest(
		t, http.MethodPatch, "/api/v1/projects/42/canvas/nodes/n1",
		[]byte(`{"ownerToken":"stale","patch":{"settings":{"prompt":"new"}}}`),
		map[string]string{"nodeID": "n1"},
	))

	if rec.Code != http.StatusConflict {
		t.Fatalf("个人身份失锁 status = %d, want 409", rec.Code)
	}
	if owner.requiredToken != "stale" {
		t.Fatalf("个人路径没调 Require（token=%q）", owner.requiredToken)
	}
	if repo.mutateCalled {
		t.Fatal("个人身份失锁后仍然写了库")
	}
}

// 空 ownerToken 的个人请求同样必须被拒——「没带凭据」不能因为并发编辑改动变成放行。
func TestCanvasMutationPersonalIdentityRejectsEmptyToken(t *testing.T) {
	repo := &fakeProjectRepository{
		project:       &projects.Project{ID: 42, UserID: 7},
		mutatePayload: json.RawMessage(`{"nodes":[{"id":"n1","type":"text"}],"connections":[]}`),
	}
	owner := &fakeCanvasOwner{requireErr: canvasowner.ErrOwnerExpired}
	handler := NewCanvasMutationHandler(repo, owner, nil)
	rec := httptest.NewRecorder()

	handler.PatchNode(rec, newProjectCanvasRequest(
		t, http.MethodPatch, "/api/v1/projects/42/canvas/nodes/n1",
		[]byte(`{"patch":{"settings":{"prompt":"new"}}}`),
		map[string]string{"nodeID": "n1"},
	))

	if rec.Code != http.StatusConflict {
		t.Fatalf("个人身份无 token status = %d, want 409", rec.Code)
	}
	if repo.mutateCalled {
		t.Fatal("个人身份无 token 却写了库")
	}
}

// 每个端点广播的 op.kind 必须与端点一一对应，且带上对端本地重放所需的数据。
// 对错了对端会用错误的语义合并（比如把 patch 当整节点覆盖，抹掉同伴的字段）。
func TestCanvasMutationBroadcastsOpPerEndpoint(t *testing.T) {
	const snapshot = `{"nodes":[{"id":"n1","type":"text"},{"id":"n2","type":"text"}],"connections":[{"id":"c1","from":"n1","to":"n2"}]}`

	newHandler := func() (*CanvasMutationHandler, *recordingNotifier) {
		repo := &fakeProjectRepository{
			project:       &projects.Project{ID: 42, UserID: 7},
			mutatePayload: json.RawMessage(snapshot),
			mutateVersion: 4,
		}
		notifier := &recordingNotifier{}
		h := NewCanvasMutationHandler(repo, &fakeCanvasOwner{}, nil)
		h.SetChangeNotifier(notifier)
		return h, notifier
	}

	t.Run("upsertNode", func(t *testing.T) {
		h, n := newHandler()
		h.UpsertNode(httptest.NewRecorder(), teamCanvasRequest(t, http.MethodPut,
			"/api/v1/projects/42/canvas/nodes", []byte(`{"ownerToken":"team:aa","node":{"id":"n3","type":"text"}}`), nil, 9))
		op := requireOp(t, n)
		if op.Kind != canvasstate.OpUpsertNode || len(op.Node) == 0 {
			t.Fatalf("op = %+v", op)
		}
	})

	t.Run("patchNode", func(t *testing.T) {
		h, n := newHandler()
		h.PatchNode(httptest.NewRecorder(), teamCanvasRequest(t, http.MethodPatch,
			"/api/v1/projects/42/canvas/nodes/n1", []byte(`{"ownerToken":"team:aa","patch":{"settings":{"a":1}}}`),
			map[string]string{"nodeID": "n1"}, 9))
		op := requireOp(t, n)
		if op.Kind != canvasstate.OpPatchNode || op.NodeID != "n1" || string(op.Patch) != `{"settings":{"a":1}}` {
			t.Fatalf("op = %+v patch=%s", op, op.Patch)
		}
	})

	t.Run("deleteNodes", func(t *testing.T) {
		h, n := newHandler()
		h.DeleteNodes(httptest.NewRecorder(), teamCanvasRequest(t, http.MethodPost,
			"/api/v1/projects/42/canvas/nodes/delete", []byte(`{"ownerToken":"team:aa","nodeIds":["n2"]}`), nil, 9))
		op := requireOp(t, n)
		if op.Kind != canvasstate.OpDeleteNodes || len(op.NodeIDs) != 1 || op.NodeIDs[0] != "n2" {
			t.Fatalf("op = %+v", op)
		}
	})

	t.Run("patchPositions", func(t *testing.T) {
		h, n := newHandler()
		h.PatchNodePositions(httptest.NewRecorder(), teamCanvasRequest(t, http.MethodPatch,
			"/api/v1/projects/42/canvas/nodes/positions", []byte(`{"ownerToken":"team:aa","nodes":[{"id":"n1","x":3,"y":4}]}`), nil, 9))
		op := requireOp(t, n)
		if op.Kind != canvasstate.OpPatchPositions || len(op.Positions) != 1 || op.Positions[0].ID != "n1" {
			t.Fatalf("op = %+v", op)
		}
		if op.Positions[0].X == nil || *op.Positions[0].X != 3 || op.Positions[0].Width != nil {
			t.Fatalf("positions = %+v, want 只带 x/y", op.Positions[0])
		}
	})

	t.Run("upsertConnection", func(t *testing.T) {
		h, n := newHandler()
		h.UpsertConnection(httptest.NewRecorder(), teamCanvasRequest(t, http.MethodPut,
			"/api/v1/projects/42/canvas/connections", []byte(`{"ownerToken":"team:aa","connection":{"id":"c2","from":"n1","to":"n2"}}`), nil, 9))
		op := requireOp(t, n)
		if op.Kind != canvasstate.OpUpsertConnection || len(op.Connection) == 0 {
			t.Fatalf("op = %+v", op)
		}
	})

	t.Run("deleteConnection", func(t *testing.T) {
		h, n := newHandler()
		h.DeleteConnection(httptest.NewRecorder(), teamCanvasRequest(t, http.MethodDelete,
			"/api/v1/projects/42/canvas/connections/c1", []byte(`{"ownerToken":"team:aa"}`),
			map[string]string{"connectionID": "c1"}, 9))
		op := requireOp(t, n)
		if op.Kind != canvasstate.OpDeleteConnection || op.ConnectionID != "c1" {
			t.Fatalf("op = %+v", op)
		}
	})
}

func requireOp(t *testing.T, n *recordingNotifier) *canvasstate.CanvasOpSummary {
	t.Helper()
	if len(n.calls) != 1 {
		t.Fatalf("广播 %d 次, want 1", len(n.calls))
	}
	if n.calls[0].op == nil {
		t.Fatal("广播没带 op 摘要——对端只能退回整拉快照，本地交互态会被清掉")
	}
	return n.calls[0].op
}
