package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/projects"
)

func TestProjectHandlerGetSnapshotIncludesVersion(t *testing.T) {
	repo := &fakeProjectRepository{
		project: &projects.Project{ID: 42, UserID: 7, Name: "demo"},
		snapshot: &projects.Snapshot{
			Payload:  json.RawMessage(`{"nodes":[]}`),
			SyncedAt: time.Unix(123, 0).UTC(),
			Version:  6,
		},
	}
	handler := NewProjectHandler(repo, nil, nil, nil, nil, nil, nil, nil, nil)
	rec := httptest.NewRecorder()

	handler.GetSnapshot(rec, newProjectSnapshotRequest(t, http.MethodGet, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body struct {
		Payload json.RawMessage `json:"payload"`
		Version int64           `json:"version"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Version != 6 {
		t.Fatalf("version = %d, want 6", body.Version)
	}
	if string(body.Payload) != `{"nodes":[]}` {
		t.Fatalf("payload = %s, want original payload", body.Payload)
	}
}
