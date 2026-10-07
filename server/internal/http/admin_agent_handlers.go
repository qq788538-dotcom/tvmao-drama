package http

import (
	nethttp "net/http"

	"github.com/james/huabu_studio/apps/api/internal/agent"
)

// AdminAgentHandler 只读列出 Agent 骨干模型（P5）。Agent 模型写死为内置值（2026-10-07 用户要求），
// 任何地方都不提供切换 / 恢复默认的写接口。
type AdminAgentHandler struct {
	Repo agent.BackboneRepo
}

func (h *AdminAgentHandler) List(w nethttp.ResponseWriter, _ *nethttp.Request) {
	out, err := h.Repo.List()
	if err != nil {
		writeError(w, nethttp.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"data": out})
}
