package http

import (
	"net/http"

	"github.com/james/huabu_studio/apps/api/internal/library"
)

// AdminGCHandler 是 POST /api/v1/admin/gc：手动触发一次孤儿清扫。清扫逻辑在 library.OrphanSweeper，
// sidecar 也用它在启动时和每天自动清扫；这个入口留着便于排查。
type AdminGCHandler struct {
	sweeper *library.OrphanSweeper
}

func NewAdminGCHandler(sweeper *library.OrphanSweeper) *AdminGCHandler {
	return &AdminGCHandler{sweeper: sweeper}
}

// Run 删除早于宽限期（7 天）且复核后仍无引用的孤儿对象。?dryRun=1 只返回候选列表，不碰文件。
func (h *AdminGCHandler) Run(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("dryRun") == "1" {
		list, err := h.sweeper.Candidates(library.DefaultGracePeriod)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"candidates": list,
			"count":      len(list),
			"dryRun":     true,
		})
		return
	}
	res, err := h.sweeper.Sweep(r.Context(), library.DefaultGracePeriod)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":         res.Deleted,
		"skipped":         res.Skipped,
		"totalCandidates": res.TotalCandidates,
	})
}
