package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/canvasowner"
	"github.com/james/huabu_studio/apps/api/internal/canvasstate"
	"github.com/james/huabu_studio/apps/api/internal/projects"
)

// recordingNotifier 记下广播调用，用来钉住「mutation 成功才发帧、帧里只有指纹不含
// token、op 摘要与端点一一对应」。
type notifyCall struct {
	userID    int64
	projectID int64
	version   int64
	mark      string
	op        *canvasstate.CanvasOpSummary
}

type recordingNotifier struct {
	calls []notifyCall
}

func (r *recordingNotifier) NotifyCanvasChanged(userID, projectID, version int64, sourceMark string, op *canvasstate.CanvasOpSummary) {
	r.calls = append(r.calls, notifyCall{userID, projectID, version, sourceMark, op})
}

func probeBody(t *testing.T, rec *httptest.ResponseRecorder) canvasOwnerProbeResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body canvasOwnerProbeResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func TestCanvasOwnerProbeReportsUnheld(t *testing.T) {
	repo := &fakeProjectRepository{project: &projects.Project{ID: 42, UserID: 7}}
	handler := NewCanvasMutationHandler(repo, &fakeCanvasOwner{}, nil)
	rec := httptest.NewRecorder()

	handler.ProbeOwner(rec, newProjectCanvasRequest(t, http.MethodGet, "/api/v1/projects/42/canvas-owner/probe", nil, nil))

	got := probeBody(t, rec)
	if got.Held || got.IsSelf || got.EditorActorID != 0 {
		t.Fatalf("probe = %+v, want 全零", got)
	}
}

// 团队身份：锁由**另一个**真人成员持有 → isSelf=false（前端据此进观看模式）。
// 团队项目下 userID 是全团队共用的影子账号，只比它的话这里会误判成自己人。
func TestCanvasOwnerProbeTeammateHoldsLock(t *testing.T) {
	repo := &fakeProjectRepository{project: &projects.Project{ID: 42, UserID: 7}}
	owner := &fakeCanvasOwner{probeResult: canvasowner.ProbeResult{Held: true, UserID: 7, ActorUserID: 9, StaleSeconds: 3600}}
	handler := NewCanvasMutationHandler(repo, owner, nil)
	rec := httptest.NewRecorder()

	req := newProjectCanvasRequest(t, http.MethodGet, "/api/v1/projects/42/canvas-owner/probe", nil, nil)
	handler.ProbeOwner(rec, req.WithContext(withActorUserID(req.Context(), 5)))

	got := probeBody(t, rec)
	if !got.Held || got.IsSelf || got.EditorActorID != 9 {
		t.Fatalf("probe = %+v, want held=true isSelf=false editorActorId=9", got)
	}
	// staleSeconds 必须原样透出：锁 24h 不释放，前端只能靠它判断对方是不是早走了。
	if got.StaleSeconds != 3600 {
		t.Fatalf("staleSeconds = %d, want 3600", got.StaleSeconds)
	}
}

func TestCanvasOwnerProbeSelfHoldsLock(t *testing.T) {
	repo := &fakeProjectRepository{project: &projects.Project{ID: 42, UserID: 7}}
	owner := &fakeCanvasOwner{probeResult: canvasowner.ProbeResult{Held: true, UserID: 7, ActorUserID: 9}}
	handler := NewCanvasMutationHandler(repo, owner, nil)
	rec := httptest.NewRecorder()

	req := newProjectCanvasRequest(t, http.MethodGet, "/api/v1/projects/42/canvas-owner/probe", nil, nil)
	handler.ProbeOwner(rec, req.WithContext(withActorUserID(req.Context(), 9)))

	got := probeBody(t, rec)
	if !got.Held || !got.IsSelf {
		t.Fatalf("probe = %+v, want held=true isSelf=true", got)
	}
}

// 个人身份（含热升级期间的两段式旧锁值）：actor 恒 0，自己人判定退回比 userID。
func TestCanvasOwnerProbePersonalLockComparesUserID(t *testing.T) {
	repo := &fakeProjectRepository{project: &projects.Project{ID: 42, UserID: 7}}
	owner := &fakeCanvasOwner{probeResult: canvasowner.ProbeResult{Held: true, UserID: 7}}
	handler := NewCanvasMutationHandler(repo, owner, nil)
	rec := httptest.NewRecorder()

	handler.ProbeOwner(rec, newProjectCanvasRequest(t, http.MethodGet, "/api/v1/projects/42/canvas-owner/probe", nil, nil))

	got := probeBody(t, rec)
	if !got.Held || !got.IsSelf || got.EditorActorID != 0 {
		t.Fatalf("probe = %+v, want held=true isSelf=true editorActorId=0", got)
	}
}

func TestCanvasOwnerProbeUnavailableReturnsServiceUnavailable(t *testing.T) {
	repo := &fakeProjectRepository{project: &projects.Project{ID: 42, UserID: 7}}
	owner := &fakeCanvasOwner{probeErr: errRedisDown}
	handler := NewCanvasMutationHandler(repo, owner, nil)
	rec := httptest.NewRecorder()

	handler.ProbeOwner(rec, newProjectCanvasRequest(t, http.MethodGet, "/api/v1/projects/42/canvas-owner/probe", nil, nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// Claim 把 ctx 里的真人成员 id 编进锁值——没有它 Probe 永远只能回答「有人在编辑」，
// 回答不了「是谁」。个人身份下这个值是 0，与升级前逐字一致。
func TestCanvasOwnerClaimPassesActorFromContext(t *testing.T) {
	repo := &fakeProjectRepository{project: &projects.Project{ID: 42, UserID: 7}}
	owner := &fakeCanvasOwner{}
	handler := NewCanvasMutationHandler(repo, owner, nil)

	req := newProjectCanvasRequest(t, http.MethodPost, "/api/v1/projects/42/canvas/owner/claim", nil, nil)
	handler.ClaimOwner(httptest.NewRecorder(), req.WithContext(withActorUserID(req.Context(), 9)))
	if owner.claimUserID != 7 || owner.claimActorID != 9 {
		t.Fatalf("Claim(user=%d, actor=%d), want (7, 9)", owner.claimUserID, owner.claimActorID)
	}

	owner2 := &fakeCanvasOwner{}
	handler2 := NewCanvasMutationHandler(repo, owner2, nil)
	handler2.ClaimOwner(httptest.NewRecorder(), newProjectCanvasRequest(t, http.MethodPost, "/api/v1/projects/42/canvas/owner/claim", nil, nil))
	if owner2.claimUserID != 7 || owner2.claimActorID != 0 {
		t.Fatalf("个人身份 Claim(user=%d, actor=%d), want (7, 0)", owner2.claimUserID, owner2.claimActorID)
	}
}

// 写成功 → 广播一帧，sourceMark 只是 token 末段的前 8 位：完整 token 会让任何
// 观看者拿到编辑权凭据。
func TestCanvasMutationNotifiesOnSuccess(t *testing.T) {
	repo := &fakeProjectRepository{
		project:       &projects.Project{ID: 42, UserID: 7},
		mutatePayload: json.RawMessage(`{"nodes":[{"id":"n1","type":"text","data":{}}],"connections":[]}`),
		mutateVersion: 8,
	}
	notifier := &recordingNotifier{}
	handler := NewCanvasMutationHandler(repo, &fakeCanvasOwner{}, nil)
	handler.SetChangeNotifier(notifier)
	rec := httptest.NewRecorder()

	handler.PatchNode(rec, newProjectCanvasRequest(
		t, http.MethodPatch, "/api/v1/projects/42/canvas/nodes/n1",
		[]byte(`{"ownerToken":"7:9:0123456789abcdef","patch":{"data":{"prompt":"new"}}}`),
		map[string]string{"nodeID": "n1"},
	))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("广播 %d 次, want 1", len(notifier.calls))
	}
	got := notifier.calls[0]
	if got.userID != 7 || got.projectID != 42 || got.version != 8 || got.mark != "01234567" {
		t.Fatalf("广播参数 %+v, want user=7 project=42 version=8 mark=01234567", got)
	}
}

// 失锁被拒 → 什么都没写，绝不能发帧（否则全体观看端白拉快照）。
func TestCanvasMutationDoesNotNotifyWhenOwnerLost(t *testing.T) {
	repo := &fakeProjectRepository{
		project:       &projects.Project{ID: 42, UserID: 7},
		mutatePayload: json.RawMessage(`{"nodes":[{"id":"n1","type":"text","data":{}}],"connections":[]}`),
	}
	notifier := &recordingNotifier{}
	handler := NewCanvasMutationHandler(repo, &fakeCanvasOwner{requireErr: canvasowner.ErrOwnerLost}, nil)
	handler.SetChangeNotifier(notifier)

	handler.PatchNode(httptest.NewRecorder(), newProjectCanvasRequest(
		t, http.MethodPatch, "/api/v1/projects/42/canvas/nodes/n1",
		[]byte(`{"ownerToken":"stale","patch":{"data":{"prompt":"new"}}}`),
		map[string]string{"nodeID": "n1"},
	))

	if len(notifier.calls) != 0 {
		t.Fatalf("失锁还广播了 %d 次", len(notifier.calls))
	}
}

// 未装配 notifier（既有 wiring / 单测）时写入照常，不 panic。
func TestCanvasMutationWithoutNotifierStillWrites(t *testing.T) {
	repo := &fakeProjectRepository{
		project:       &projects.Project{ID: 42, UserID: 7},
		mutatePayload: json.RawMessage(`{"nodes":[{"id":"n1","type":"text","data":{}}],"connections":[]}`),
		mutateVersion: 3,
	}
	handler := NewCanvasMutationHandler(repo, &fakeCanvasOwner{}, nil)
	rec := httptest.NewRecorder()

	handler.PatchNode(rec, newProjectCanvasRequest(
		t, http.MethodPatch, "/api/v1/projects/42/canvas/nodes/n1",
		[]byte(`{"ownerToken":"7:0:abcdef0123456789","patch":{"data":{"prompt":"new"}}}`),
		map[string]string{"nodeID": "n1"},
	))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !repo.mutateCalled {
		t.Fatal("没有 notifier 时也必须照常写入")
	}
}

// errRedisDown 模拟锁存储不可用（既有 owner 端点用同一套 503 错误模型）。
var errRedisDown = errors.New("redis down")
