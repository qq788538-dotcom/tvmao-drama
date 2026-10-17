package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/cliapi"
)

// CLIHandler 是 /api/v1/cli/* 上项目、画布、节点、连线的 handler。
//
// 它只做「解析请求 → 调 cliapi.Service → 渲染」，不含业务逻辑：
// 领域规则（归属校验、类型可写性、模型必填）全在 cliapi 里，便于不起 HTTP 就测。
type CLIHandler struct {
	svc *cliapi.Service
	// idem 可以为 nil（测试或未接 DB 时）：那样写请求就不去重，
	// 但 CLI 侧也不会自动重试它们，语义仍然安全。
	idem cliapi.IdempotencyStore
}

func NewCLIHandler(svc *cliapi.Service, idem cliapi.IdempotencyStore) *CLIHandler {
	return &CLIHandler{svc: svc, idem: idem}
}

// cliProjectID 从 URL 取项目 id。非法 id 直接给 PROJECT_NOT_FOUND 而不是 400——
// 对调用方来说「/projects/abc」和「/projects/999999」是同一类问题：这个项目找不到。
func cliProjectID(r *http.Request) (int64, error) {
	raw := chi.URLParam(r, "projectID")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, cliapi.Errf(cliapi.CodeProjectNotFound, "项目 %s 不存在", raw)
	}
	return id, nil
}

func (h *CLIHandler) ListProjects(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListProjects(userIDFromContext(r.Context()))
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, map[string]any{"data": list})
}

func (h *CLIHandler) GetProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	p, err := h.svc.GetProject(userIDFromContext(r.Context()), projectID)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, p)
}

func (h *CLIHandler) GetCanvas(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	c, err := h.svc.GetCanvas(userIDFromContext(r.Context()), projectID)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, c)
}

func (h *CLIHandler) ListNodes(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	list, err := h.svc.ListNodes(userIDFromContext(r.Context()), projectID, cliapi.NodeFilter{
		Type:   r.URL.Query().Get("type"),
		Status: r.URL.Query().Get("status"),
	})
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, map[string]any{"data": list})
}

func (h *CLIHandler) GetNode(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	n, err := h.svc.GetNodeDetail(userIDFromContext(r.Context()), projectID, chi.URLParam(r, "nodeID"))
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, n)
}

// ResolveNode 把节点引用解析成 ID。
//
// CLI 的所有 mutation 都先走这里再按 ID 操作，这样节点引用即使包含 `/`、空格
// 或非 ASCII 字符也不会进 URL path。
func (h *CLIHandler) ResolveNode(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	id, err := h.svc.ResolveNode(userIDFromContext(r.Context()), projectID, r.URL.Query().Get("ref"))
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, map[string]any{"nodeId": id})
}

func (h *CLIHandler) ListEdges(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	list, err := h.svc.ListEdges(userIDFromContext(r.Context()), projectID)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, map[string]any{"data": list})
}
