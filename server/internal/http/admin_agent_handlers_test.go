package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/agent"
	"github.com/james/huabu_studio/apps/api/internal/models"
)

type fakeBackbones struct {
	rows    map[int64]agent.BackboneModel
	updated bool
}

func (f *fakeBackbones) Create(agent.BackboneModel) (int64, error) { return 0, errors.New("no") }
func (f *fakeBackbones) Update(agent.BackboneModel) error          { f.updated = true; return nil }
func (f *fakeBackbones) Delete(int64) error                        { return errors.New("no") }
func (f *fakeBackbones) Get(id int64) (*agent.BackboneModel, error) {
	m, ok := f.rows[id]
	if !ok {
		return nil, agent.ErrBackboneNotFound
	}
	return &m, nil
}
func (f *fakeBackbones) List() ([]agent.BackboneModel, error) {
	out := []agent.BackboneModel{}
	for _, m := range f.rows {
		out = append(out, m)
	}
	return out, nil
}
func (f *fakeBackbones) GetActiveByRole(string) (*agent.BackboneModel, error) {
	return nil, agent.ErrNoActiveBackbone
}

type fakeSwitcher struct{ repo *fakeBackbones }

func (s fakeSwitcher) SetModelName(id int64, name string) error {
	m, ok := s.repo.rows[id]
	if !ok {
		return agent.ErrBackboneNotFound
	}
	m.ModelName = name
	s.repo.rows[id] = m
	return nil
}
func (s fakeSwitcher) ResetModelName(id int64, name string) error { return s.SetModelName(id, name) }

type fakeModelLookup map[string]models.ModelConfig

func (f fakeModelLookup) GetByModelID(id string) (*models.ModelConfig, error) {
	m, ok := f[id]
	if !ok {
		return nil, models.ErrNotFound
	}
	return &m, nil
}

func newAdminAgent() (*AdminAgentHandler, *fakeBackbones) {
	repo := &fakeBackbones{rows: map[int64]agent.BackboneModel{1: {ID: 1, Role: agent.RolePlanner, ModelName: "canvas-agent-gpt-5.5", Temperature: 0}}}
	h := &AdminAgentHandler{
		Repo: repo, Switcher: fakeSwitcher{repo: repo},
		Models: fakeModelLookup{
			"gpt-5.5":     {ModelID: "gpt-5.5", Type: "text", Enabled: true},
			"gpt-image-2": {ModelID: "gpt-image-2", Type: "image", Enabled: true},
			"old-text":    {ModelID: "old-text", Type: "text", Enabled: false},
		},
		Defaults: func(id int64) (string, bool) { return "canvas-agent-gpt-5.5", id == 1 },
	}
	return h, repo
}

func callWithID(h http.HandlerFunc, method, id, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/x", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(withRouteCtx(req, rctx))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestAdminAgentUpdate_SwitchesToTextModelOnly(t *testing.T) {
	h, repo := newAdminAgent()
	if rec := callWithID(h.Update, http.MethodPut, "1", `{"model_name":"gpt-image-2"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("图片模型应被拒绝: %d", rec.Code)
	}
	if rec := callWithID(h.Update, http.MethodPut, "1", `{"model_name":"old-text"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("停用的文本模型应被拒绝: %d", rec.Code)
	}
	rec := callWithID(h.Update, http.MethodPut, "1", `{"model_name":"gpt-5.5","temperature":1.5,"max_tokens":9}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("切换失败: %d %s", rec.Code, rec.Body.String())
	}
	var got agent.BackboneModel
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.ModelName != "gpt-5.5" || got.Temperature != 0 || repo.updated {
		t.Fatalf("只应改 model_name: %+v updated=%v", got, repo.updated)
	}
}

func TestAdminAgentUpdate_NotFound(t *testing.T) {
	h, _ := newAdminAgent()
	if rec := callWithID(h.Update, http.MethodPut, "99", `{"model_name":"gpt-5.5"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestAdminAgentReset(t *testing.T) {
	h, repo := newAdminAgent()
	_ = fakeSwitcher{repo: repo}.SetModelName(1, "gpt-5.5")
	rec := callWithID(h.Reset, http.MethodPost, "1", ``)
	if rec.Code != http.StatusOK || repo.rows[1].ModelName != "canvas-agent-gpt-5.5" {
		t.Fatalf("恢复默认失败: %d %+v", rec.Code, repo.rows[1])
	}
	if rec := callWithID(h.Reset, http.MethodPost, "99", ``); rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func withRouteCtx(r *http.Request, rctx *chi.Context) context.Context {
	return context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
}
