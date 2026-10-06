package http

import (
	nethttp "net/http"

	"github.com/james/huabu_studio/apps/api/internal/agent"
)

// AdminAgentHandler 只读列出 Agent 骨干模型（P5）。切换 model_name 与恢复默认由设置页的
// /settings/agent-models（P4 SettingsCenter，同一个 BackboneSwitcher）负责，这里不再提供写接口。
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
