package http

import (
	"net/http"
	"regexp"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/cliapi"
)

// CLI 的写入 handler。
//
// 每个写请求都可带 Idempotency-Key：CLI 会在网络失败后重试，没有幂等就会
// 建出第二个节点、冻结第二次积分。scope 按操作种类分开，避免不同操作用同一个
// key 时互相回放。

// ifMatchRe 解析 `If-Match: "snapshot-17"`。
// 用画布快照版本而不是 ETag 哈希——版本号是画布本来就有的并发标记。
var ifMatchRe = regexp.MustCompile(`^"?snapshot-(\d+)"?$`)

func mutationOptions(r *http.Request) (cliapi.MutationOptions, error) {
	raw := r.Header.Get("If-Match")
	if raw == "" {
		return cliapi.MutationOptions{}, nil
	}
	m := ifMatchRe.FindStringSubmatch(raw)
	if m == nil {
		return cliapi.MutationOptions{}, cliapi.Errf(cliapi.CodeInvalidArgument,
			`If-Match 格式应为 "snapshot-<版本号>"，收到 %q`, raw)
	}
	v, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return cliapi.MutationOptions{}, cliapi.New(cliapi.CodeInvalidArgument, "If-Match 版本号非法")
	}
	return cliapi.MutationOptions{ExpectVersion: v}, nil
}

// runIdempotentWrite 是写 handler 的统一外壳：解析幂等键 → 执行 → 渲染。
func (h *CLIHandler) runIdempotentWrite(
	w http.ResponseWriter, r *http.Request,
	scope string, request any,
	fn func() (int, any, error),
) {
	status, body, err := cliapi.RunIdempotent(
		h.idem, userIDFromContext(r.Context()), scope,
		r.Header.Get("Idempotency-Key"), request, fn,
	)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ---- 项目 ----

func (h *CLIHandler) CreateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeCLIError(w, r, cliapi.New(cliapi.CodeInvalidArgument, "请求体不是合法 JSON"))
		return
	}
	userID := userIDFromContext(r.Context())
	h.runIdempotentWrite(w, r, "project_create", body, func() (int, any, error) {
		p, err := h.svc.CreateProject(userID, body.Name, body.Description)
		return http.StatusCreated, p, err
	})
}

func (h *CLIHandler) UpdateProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	// 指针字段：只有请求里出现的键才会被改。用值类型的话，「不传 name」
	// 与「把 name 改成空串」无法区分。
	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeCLIError(w, r, cliapi.New(cliapi.CodeInvalidArgument, "请求体不是合法 JSON"))
		return
	}
	p, err := h.svc.UpdateProject(userIDFromContext(r.Context()), projectID, body.Name, body.Description)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, p)
}

func (h *CLIHandler) DeleteProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	if err := h.svc.DeleteProject(userIDFromContext(r.Context()), projectID); err != nil {
		writeCLIError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- 节点 ----

func (h *CLIHandler) CreateNode(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	opts, err := mutationOptions(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	var body cliapi.CreateNodeRequest
	if err := decodeJSON(r, &body); err != nil {
		writeCLIError(w, r, cliapi.New(cliapi.CodeInvalidArgument, "请求体不是合法 JSON"))
		return
	}
	userID := userIDFromContext(r.Context())
	h.runIdempotentWrite(w, r, "node_create", body, func() (int, any, error) {
		n, err := h.svc.CreateNode(userID, projectID, body, opts)
		return http.StatusCreated, n, err
	})
}

func (h *CLIHandler) PatchNode(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	opts, err := mutationOptions(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	var body cliapi.PatchNodeRequest
	if err := decodeJSON(r, &body); err != nil {
		writeCLIError(w, r, cliapi.New(cliapi.CodeInvalidArgument, "请求体不是合法 JSON"))
		return
	}
	n, err := h.svc.PatchNode(userIDFromContext(r.Context()), projectID, chi.URLParam(r, "nodeID"), body, opts)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, n)
}

// PatchNodePositions PATCH /cli/projects/{projectID}/nodes/positions
//
// 不走幂等外壳：几何补丁天然幂等（同一批坐标重放结果相同），且不花钱。
func (h *CLIHandler) PatchNodePositions(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	opts, err := mutationOptions(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	var body cliapi.PatchNodePositionsRequest
	if err := decodeJSON(r, &body); err != nil {
		writeCLIError(w, r, cliapi.New(cliapi.CodeInvalidArgument, "请求体不是合法 JSON"))
		return
	}
	res, err := h.svc.PatchNodePositions(userIDFromContext(r.Context()), projectID, body, opts)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, res)
}

func (h *CLIHandler) DeleteNode(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	opts, err := mutationOptions(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	if err := h.svc.DeleteNode(userIDFromContext(r.Context()), projectID, chi.URLParam(r, "nodeID"), opts); err != nil {
		writeCLIError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- 连线 ----

func (h *CLIHandler) CreateEdge(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	opts, err := mutationOptions(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	var body cliapi.CreateEdgeRequest
	if err := decodeJSON(r, &body); err != nil {
		writeCLIError(w, r, cliapi.New(cliapi.CodeInvalidArgument, "请求体不是合法 JSON"))
		return
	}
	userID := userIDFromContext(r.Context())
	h.runIdempotentWrite(w, r, "edge_create", body, func() (int, any, error) {
		e, err := h.svc.CreateEdge(userID, projectID, body, opts)
		// 恒 200：这个端点的语义是「确保这条边存在」，新建与已存在对调用方
		// 完全一样，不值得为区分 201/200 让 service 多回一个 bool。
		return http.StatusOK, e, err
	})
}

func (h *CLIHandler) DeleteEdge(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	opts, err := mutationOptions(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	if err := h.svc.DeleteEdge(userIDFromContext(r.Context()), projectID, chi.URLParam(r, "edgeID"), opts); err != nil {
		writeCLIError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- 运行 ----

// RunNode POST /cli/projects/{projectID}/nodes/{nodeID}/runs
//
// 这是 CLI 唯一会花钱的端点，所以必须支持幂等：没有它，网络断开后的一次重试
// 就是第二次冻结积分 + 第二次跑上游。
func (h *CLIHandler) RunNode(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	nodeID := chi.URLParam(r, "nodeID")
	var body cliapi.RunRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeCLIError(w, r, cliapi.New(cliapi.CodeInvalidArgument, "请求体不是合法 JSON"))
			return
		}
	}
	userID := userIDFromContext(r.Context())
	// 幂等请求摘要要带 nodeID：同一个 key 用在两个节点上必须报冲突，
	// 否则第二个节点会静默拿到第一个节点的 taskId。
	digest := map[string]any{"nodeId": nodeID, "overrides": body.Overrides}
	h.runIdempotentWrite(w, r, "node_run", digest, func() (int, any, error) {
		res, err := h.svc.RunNode(r.Context(), userID, projectID, nodeID, body)
		return http.StatusAccepted, res, err
	})
}

// GetRun GET /cli/runs/{taskID} —— CLI 的 --wait 轮询这里，不另建状态机。
func (h *CLIHandler) GetRun(w http.ResponseWriter, r *http.Request) {
	taskID, err := strconv.ParseInt(chi.URLParam(r, "taskID"), 10, 64)
	if err != nil || taskID <= 0 {
		writeCLIError(w, r, cliapi.Errf(cliapi.CodeNodeNotFound, "任务 %s 不存在", chi.URLParam(r, "taskID")))
		return
	}
	st, err := h.svc.GetRun(r.Context(), userIDFromContext(r.Context()), taskID)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, st)
}
