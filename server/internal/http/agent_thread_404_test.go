package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/agent"
)

type missingProjectRepo struct{ stubThreadRepo }

func (missingProjectRepo) CreateThread(agent.Thread) error { return agent.ErrProjectNotFound }

func TestAgentCreateThread_MissingProject404(t *testing.T) {
	h := &AgentHandler{Repo: missingProjectRepo{}}
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
	req = req.WithContext(withProjectID(req.Context(), 999))
	rec := httptest.NewRecorder()
	h.CreateThread(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}
