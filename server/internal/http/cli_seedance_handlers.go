package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Seedance 2.0 合规校验的 CLI 端点。
//
// 网页那两个（/seedance/verify、/seedance/status）挂在网页 auth group 下，
// aud=cli 会被反向拒绝——这是刻意的凭据双向隔离，不该为了 CLI 去开口子。
// 所以这里在 /api/v1/cli/* 下另起一套：service 层复用同一个
// library.SeedanceService，网页 handler 一行不改。
//
// 入口是节点 ID 而不是网页那样的媒体 URL，理由见 cliapi/service_seedance.go。

// VerifyNodeMedia: POST /cli/projects/{projectID}/nodes/{nodeID}/compliance
//
// 202 而不是 200：提交后上游要异步跑，状态大概率还是 pending，
// 用 202 表达「收到了，还没有结论」，跟 node run 的语义一致。
func (h *CLIHandler) VerifyNodeMedia(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	st, err := h.svc.VerifyNodeMedia(
		r.Context(), userIDFromContext(r.Context()), projectID, chi.URLParam(r, "nodeID"))
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusAccepted, st)
}

// ListCompliance: GET /cli/projects/{projectID}/compliance
//
// 一次给出项目里所有带媒体节点的状态。做成整表而不是查单个：真实场景是提交完
// 一批素材后盯着它们什么时候变 active，一个个查既要 N 条命令又看不出整体进度。
func (h *CLIHandler) ListCompliance(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	list, err := h.svc.ListCompliance(userIDFromContext(r.Context()), projectID)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, list)
}
