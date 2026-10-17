package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasowner"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/projects"
)

func TestCanvasMutationPatchNodeRequiresOwner(t *testing.T) {
	repo := &fakeProjectRepository{
		project:       &projects.Project{ID: 42, UserID: 7, Name: "demo"},
		mutatePayload: json.RawMessage(`{"nodes":[{"id":"n1","data":{"prompt":"old"}}],"connections":[]}`),
	}
	owner := &fakeCanvasOwner{requireErr: canvasowner.ErrOwnerLost}
	handler := NewCanvasMutationHandler(repo, owner, nil)
	rec := httptest.NewRecorder()

	handler.PatchNode(rec, newProjectCanvasRequest(t, http.MethodPatch, "/api/v1/projects/42/canvas/nodes/n1", []byte(`{"ownerToken":"stale","patch":{"data":{"prompt":"new"}}}`), map[string]string{"nodeID": "n1"}))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "CANVAS_OWNER_LOST" {
		t.Fatalf("code = %q, want CANVAS_OWNER_LOST", body.Code)
	}
	if repo.mutateCalled {
		t.Fatalf("MutateClientSnapshot was called without current owner")
	}
}

func TestCanvasMutationPatchNodeMutatesJSONAndReturnsVersion(t *testing.T) {
	repo := &fakeProjectRepository{
		project:       &projects.Project{ID: 42, UserID: 7, Name: "demo"},
		mutatePayload: json.RawMessage(`{"nodes":[{"id":"n1","type":"text","data":{"prompt":"old","nested":{"a":1}}}],"connections":[]}`),
		mutateVersion: 8,
	}
	owner := &fakeCanvasOwner{}
	handler := NewCanvasMutationHandler(repo, owner, nil)
	rec := httptest.NewRecorder()

	handler.PatchNode(rec, newProjectCanvasRequest(t, http.MethodPatch, "/api/v1/projects/42/canvas/nodes/n1", []byte(`{"ownerToken":"token","patch":{"data":{"prompt":"new","nested":{"b":2}}}}`), map[string]string{"nodeID": "n1"}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		Version int64 `json:"version"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Version != 8 {
		t.Fatalf("version = %d, want 8", body.Version)
	}
	if owner.requiredToken != "token" {
		t.Fatalf("owner token = %q, want token", owner.requiredToken)
	}
	var snapshot struct {
		Nodes []struct {
			ID   string `json:"id"`
			Data struct {
				Prompt string         `json:"prompt"`
				Nested map[string]int `json:"nested"`
			} `json:"data"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(repo.mutatedPayload, &snapshot); err != nil {
		t.Fatalf("decode mutated payload: %v", err)
	}
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].ID != "n1" {
		t.Fatalf("nodes = %#v, want patched n1", snapshot.Nodes)
	}
	if snapshot.Nodes[0].Data.Prompt != "new" {
		t.Fatalf("prompt = %q, want new", snapshot.Nodes[0].Data.Prompt)
	}
	if snapshot.Nodes[0].Data.Nested["a"] != 1 || snapshot.Nodes[0].Data.Nested["b"] != 2 {
		t.Fatalf("nested = %#v, want merged keys", snapshot.Nodes[0].Data.Nested)
	}
}

func TestCanvasMutationClaimOwnerReturnsToken(t *testing.T) {
	expiresAt := time.Unix(456, 0).UTC()
	repo := &fakeProjectRepository{project: &projects.Project{ID: 42, UserID: 7, Name: "demo"}}
	owner := &fakeCanvasOwner{claimResult: canvasowner.ClaimResult{OwnerToken: "owner-token", ExpiresAt: expiresAt}}
	handler := NewCanvasMutationHandler(repo, owner, nil)
	rec := httptest.NewRecorder()

	handler.ClaimOwner(rec, newProjectCanvasRequest(t, http.MethodPost, "/api/v1/projects/42/canvas/owner/claim", nil, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		OwnerToken string    `json:"ownerToken"`
		ExpiresAt  time.Time `json:"expiresAt"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.OwnerToken != "owner-token" || !body.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("body = %#v, want owner token and expiry", body)
	}
	if owner.claimUserID != 7 || owner.claimProjectID != 42 {
		t.Fatalf("claim ids = user %d project %d, want 7/42", owner.claimUserID, owner.claimProjectID)
	}
}

func TestCanvasMutationUpsertConnectionMissingNodeReturnsNotFound(t *testing.T) {
	repo := &fakeProjectRepository{
		project:       &projects.Project{ID: 42, UserID: 7, Name: "demo"},
		mutatePayload: json.RawMessage(`{"nodes":[{"id":"a"}],"connections":[]}`),
	}
	handler := NewCanvasMutationHandler(repo, &fakeCanvasOwner{}, nil)
	rec := httptest.NewRecorder()

	handler.UpsertConnection(rec, newProjectCanvasRequest(t, http.MethodPost, "/api/v1/projects/42/canvas/connections", []byte(`{"ownerToken":"token","connection":{"id":"conn-1","from":"a","to":"missing"}}`), nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestCanvasMutationRejectsTrailingJSON(t *testing.T) {
	repo := &fakeProjectRepository{
		project:       &projects.Project{ID: 42, UserID: 7, Name: "demo"},
		mutatePayload: json.RawMessage(`{"nodes":[{"id":"n1"}],"connections":[]}`),
	}
	handler := NewCanvasMutationHandler(repo, &fakeCanvasOwner{}, nil)
	rec := httptest.NewRecorder()

	handler.PatchNode(rec, newProjectCanvasRequest(t, http.MethodPatch, "/api/v1/projects/42/canvas/nodes/n1", []byte(`{"ownerToken":"token","patch":{"x":1}}{"ownerToken":"token","patch":{"x":2}}`), map[string]string{"nodeID": "n1"}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if repo.mutateCalled {
		t.Fatalf("MutateClientSnapshot was called for malformed JSON")
	}
}

func TestCanvasMutationOwnerUnavailableReturnsServiceUnavailable(t *testing.T) {
	repo := &fakeProjectRepository{project: &projects.Project{ID: 42, UserID: 7, Name: "demo"}}
	handler := NewCanvasMutationHandler(repo, &fakeCanvasOwner{requireErr: errors.New("redis down")}, nil)
	rec := httptest.NewRecorder()

	handler.PatchNode(rec, newProjectCanvasRequest(t, http.MethodPatch, "/api/v1/projects/42/canvas/nodes/n1", []byte(`{"ownerToken":"token","patch":{"x":1}}`), map[string]string{"nodeID": "n1"}))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "CANVAS_OWNER_UNAVAILABLE" {
		t.Fatalf("code = %q, want CANVAS_OWNER_UNAVAILABLE", body.Code)
	}
	if body.Error == "redis down" {
		t.Fatalf("error leaked internal owner failure")
	}
}

func TestCanvasMutationClaimOwnerUnavailableReturnsServiceUnavailable(t *testing.T) {
	repo := &fakeProjectRepository{project: &projects.Project{ID: 42, UserID: 7, Name: "demo"}}
	handler := NewCanvasMutationHandler(repo, &fakeCanvasOwner{claimErr: errors.New("redis down")}, nil)
	rec := httptest.NewRecorder()

	handler.ClaimOwner(rec, newProjectCanvasRequest(t, http.MethodPost, "/api/v1/projects/42/canvas/owner/claim", nil, nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "CANVAS_OWNER_UNAVAILABLE" {
		t.Fatalf("code = %q, want CANVAS_OWNER_UNAVAILABLE", body.Code)
	}
}

type fakeCanvasOwner struct {
	claimResult    canvasowner.ClaimResult
	claimErr       error
	claimProjectID int64
	claimUserID    int64
	claimActorID   int64
	heartbeatErr   error
	heartbeatToken string
	requireErr     error
	requiredToken  string
	probeResult    canvasowner.ProbeResult
	probeErr       error
}

func (f *fakeCanvasOwner) Claim(_ context.Context, projectID int64, userID int64, actorUserID int64) (canvasowner.ClaimResult, error) {
	f.claimProjectID = projectID
	f.claimUserID = userID
	f.claimActorID = actorUserID
	if f.claimErr != nil {
		return canvasowner.ClaimResult{}, f.claimErr
	}
	return f.claimResult, nil
}

func (f *fakeCanvasOwner) Probe(_ context.Context, _ int64) (canvasowner.ProbeResult, error) {
	return f.probeResult, f.probeErr
}

func (f *fakeCanvasOwner) Heartbeat(_ context.Context, _ int64, ownerToken string) error {
	f.heartbeatToken = ownerToken
	return f.heartbeatErr
}

func (f *fakeCanvasOwner) Require(_ context.Context, _ int64, ownerToken string) error {
	f.requiredToken = ownerToken
	return f.requireErr
}
