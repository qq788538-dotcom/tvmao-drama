package http

import (
	"errors"
	nethttp "net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/agent"
)

// AdminAgentHandler 是设置页"Agent 模型"（spec A2、M8）的后端：大模型全部内置，
// 用户只能把某个角色的 model_name 换成另一个内置的已启用文本模型，或恢复默认。
type AdminAgentHandler struct {
	Repo     agent.BackboneRepo
	Switcher agent.BackboneModelSwitcher
	Models   agent.ModelLookup
	// Defaults 返回某行的内置 model_name（装配时传 agentseed.DefaultBackboneModelName）。
	Defaults func(id int64) (string, bool)
}

func (h *AdminAgentHandler) List(w nethttp.ResponseWriter, _ *nethttp.Request) {
	out, err := h.Repo.List()
	if err != nil {
		writeError(w, nethttp.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"data": out})
}

// Update 只认 body 里的 model_name；其余字段（参考仓后台页会整行提交）一律忽略。
func (h *AdminAgentHandler) Update(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, "bad id")
		return
	}
	var body struct {
		ModelName string `json:"model_name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, nethttp.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(body.ModelName)
	if m, merr := h.Models.GetByModelID(name); name == "" || merr != nil || m == nil || m.Type != "text" || !m.Enabled {
		writeError(w, nethttp.StatusBadRequest, "只能切换到内置的已启用文本模型")
		return
	}
	h.applyAndRespond(w, id, h.Switcher.SetModelName(id, name))
}

func (h *AdminAgentHandler) Reset(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, "bad id")
		return
	}
	def, ok := h.Defaults(id)
	if !ok {
		writeError(w, nethttp.StatusNotFound, "not found")
		return
	}
	h.applyAndRespond(w, id, h.Switcher.ResetModelName(id, def))
}

func (h *AdminAgentHandler) applyAndRespond(w nethttp.ResponseWriter, id int64, err error) {
	if errors.Is(err, agent.ErrBackboneNotFound) {
		writeError(w, nethttp.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, nethttp.StatusInternalServerError, err.Error())
		return
	}
	m, err := h.Repo.Get(id)
	if err != nil {
		writeError(w, nethttp.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, nethttp.StatusOK, m)
}
