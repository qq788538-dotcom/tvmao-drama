package http

import (
	nethttp "net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func init() { settingsRouteRegistrars = append(settingsRouteRegistrars, registerAgentModelSettings) }

func registerAgentModelSettings(r chi.Router, d SettingsCenterDeps) {
	if d.Agent == nil || d.Models == nil {
		return
	}
	h := &agentModelSettingsHandler{d: d}
	r.Get("/agent-models", h.list)
	r.Put("/agent-models/{id}", h.switchModel)
	r.Post("/agent-models/{id}/restore-default", h.restore)
}

type agentModelSettingsHandler struct{ d SettingsCenterDeps }

type agentCandidate struct {
	ModelID string `json:"modelId"`
	Name    string `json:"name"`
}

// candidates 是 Agent 可选的模型：模型中心里已启用的文本模型（spec A2）。
func (h *agentModelSettingsHandler) candidates() ([]agentCandidate, error) {
	list, err := h.d.Models.List(false)
	if err != nil {
		return nil, err
	}
	out := []agentCandidate{}
	for _, m := range list {
		if m.Type == "text" && m.Enabled {
			out = append(out, agentCandidate{ModelID: m.ModelID, Name: m.Name})
		}
	}
	return out, nil
}

func (h *agentModelSettingsHandler) writeList(w nethttp.ResponseWriter, r *nethttp.Request) {
	items, err := h.d.Agent.ListAgentModels(r.Context())
	if err != nil {
		scWriteError(w, err)
		return
	}
	cands, err := h.candidates()
	if err != nil {
		scWriteError(w, err)
		return
	}
	if items == nil {
		items = []AgentModelItem{}
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"data": items, "candidates": cands})
}

func (h *agentModelSettingsHandler) list(w nethttp.ResponseWriter, r *nethttp.Request) {
	h.writeList(w, r)
}

func (h *agentModelSettingsHandler) switchModel(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := parseSettingsID(r)
	if err != nil {
		scWriteError(w, err)
		return
	}
	var body struct {
		ModelName string `json:"modelName"`
	}
	if err := scDecodeStrict(w, r, &body); err != nil {
		scWriteError(w, err)
		return
	}
	name := strings.TrimSpace(body.ModelName)
	cands, err := h.candidates()
	if err != nil {
		scWriteError(w, err)
		return
	}
	ok := false
	for _, c := range cands {
		if c.ModelID == name {
			ok = true
		}
	}
	if !ok {
		scWriteError(w, scValidation("modelName", "只能选择模型中心里已启用的文本模型"))
		return
	}
	if err := h.d.Agent.SwitchAgentModel(r.Context(), id, name); err != nil {
		scWriteError(w, err)
		return
	}
	h.writeList(w, r)
}

func (h *agentModelSettingsHandler) restore(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := parseSettingsID(r)
	if err != nil {
		scWriteError(w, err)
		return
	}
	if err := h.d.Agent.RestoreAgentModelDefault(r.Context(), id); err != nil {
		scWriteError(w, err)
		return
	}
	h.writeList(w, r)
}
