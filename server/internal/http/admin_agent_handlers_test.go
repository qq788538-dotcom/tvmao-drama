package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/agent"
)

type fakeBackbones struct {
	rows map[int64]agent.BackboneModel
}

func (f *fakeBackbones) Create(agent.BackboneModel) (int64, error) { return 0, errors.New("no") }
func (f *fakeBackbones) Update(agent.BackboneModel) error          { return errors.New("no") }
func (f *fakeBackbones) Delete(int64) error                        { return errors.New("no") }
func (f *fakeBackbones) Get(id int64) (*agent.BackboneModel, error) {
	m, ok := f.rows[id]
	if !ok {
		return nil, errors.New("not found")
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

func TestAdminAgentList(t *testing.T) {
	repo := &fakeBackbones{rows: map[int64]agent.BackboneModel{1: {ID: 1, Role: agent.RolePlanner, ModelName: "canvas-agent-gpt-5.5"}}}
	h := &AdminAgentHandler{Repo: repo}
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var out struct {
		Data []agent.BackboneModel `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Data) != 1 || out.Data[0].ModelName != "canvas-agent-gpt-5.5" {
		t.Fatalf("body=%s err=%v", rec.Body.String(), err)
	}
}
