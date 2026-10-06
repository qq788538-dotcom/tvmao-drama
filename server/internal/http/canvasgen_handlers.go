package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
	"github.com/james/huabu_studio/apps/api/internal/generation"
)

// CanvasGenHandler 是自由画布 AI 生成的入口:恒异步入队 + 前端轮询任务状态。
// 返回的 taskId 即 generation task id(v5 批次二:上层表已删,id 空间统一)。
type CanvasGenHandler struct {
	svc    *canvasgen.Service
	signer mediaRefSigner
}

func NewCanvasGenHandler(svc *canvasgen.Service, signer mediaRefSigner) *CanvasGenHandler {
	return &CanvasGenHandler{svc: svc, signer: signer}
}

type canvasGenSubmitBody struct {
	ProjectID int64          `json:"projectId"`
	NodeID    string         `json:"nodeId"`
	ModelID   string         `json:"modelId"`
	Inputs    map[string]any `json:"inputs"`
	// PresetKey 绘图提示词预设（可选，空=未选）。前端只传 key——预设正文对前端保密，
	// key → 正文的替换在 generation 执行层完成，inputs.prompt 里永远是纯用户输入。
	PresetKey string `json:"presetKey"`
	// PresetParams 预设参数选中档位（维度 key → 档位 key）。缺失/非法维度服务端落默认档。
	PresetParams map[string]string `json:"presetParams"`
}

// Submit 入队一次画布生成,立即返回画布任务 id(前端据此轮询)。不阻塞:执行体在后台持名额跑。
func (h *CanvasGenHandler) Submit(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	var body canvasGenSubmitBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.ProjectID == 0 || body.NodeID == "" || body.ModelID == "" {
		writeError(w, http.StatusBadRequest, "projectId/nodeId/modelId required")
		return
	}
	if body.Inputs == nil {
		body.Inputs = map[string]any{}
	}
	// 入库前把 inputs 里的媒体引用统一成 canonical media://key：桌面端签名 URL 带本次启动的
	// 端口和 token，排队中的任务跨重启后旧 URL 就失效了；执行时 prepareExecution 再现签。
	if h.signer != nil {
		if canon, ok := assets.WalkStrings(body.Inputs, h.signer.Canonicalize).(map[string]any); ok {
			body.Inputs = canon
		}
	}
	taskID, err := h.svc.Enqueue(r.Context(), canvasgen.EnqueueInput{
		UserID:       userID,
		ProjectID:    body.ProjectID,
		NodeID:       body.NodeID,
		ModelID:      body.ModelID,
		Inputs:       body.Inputs,
		PresetKey:    body.PresetKey,
		PresetParams: body.PresetParams,
	})
	if err != nil {
		// 用户可自解的前置条件问题给 400,不是网关故障。
		// 预设的图片数约束不满足：连线用户自己能改，同 InputLimitError 一队 400。
		var presetErr *canvasgen.PresetConstraintError
		if errors.As(err, &presetErr) {
			writeError(w, http.StatusBadRequest, presetErr.Error())
			return
		}
		if errors.Is(err, generation.ErrDataURIInput) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// 输入数量违约：连线数用户自己能改，400 而非 502；文案原样透出。
		var limitErr *generation.InputLimitError
		if errors.As(err, &limitErr) {
			writeError(w, http.StatusBadRequest, limitErr.Error())
			return
		}
		// 输入视频时长不可读：换视频/重传可自解，与数量违约同队 400，文案原样透出。
		var durErr *generation.InputDurationError
		if errors.As(err, &durErr) {
			writeError(w, http.StatusBadRequest, durErr.Error())
			return
		}
		// 存储已满：提交时拦（产物落库永远放行），409 与上传闸同语义，
		// 前端透出 error 文案即可行动（清理素材）。
		if errors.Is(err, generation.ErrStorageQuotaExceeded) {
			writeError(w, http.StatusConflict, "存储空间已满，请清理后重试再生成")
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"taskId": taskID, "status": "processing"})
}

// NodeStatus 按 (projectId, nodeId) 找回该画布节点最新一次任务——「节点卡在
// generating 但本地 taskId 丢失」的前端自愈对账入口。查无返回 404:对前端这是
// 有效信号(连续查无若干轮才把节点判死),必须与服务器故障的 5xx 区分开。
func (h *CanvasGenHandler) NodeStatus(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	projectID, err := strconv.ParseInt(r.URL.Query().Get("projectId"), 10, 64)
	nodeID := r.URL.Query().Get("nodeId")
	if err != nil || projectID <= 0 || nodeID == "" {
		writeError(w, http.StatusBadRequest, "projectId/nodeId required")
		return
	}
	st, err := h.svc.GetNodeStatus(r.Context(), userID, projectID, nodeID)
	if err != nil {
		if errors.Is(err, generation.ErrNotFound) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.signer != nil {
		for i, u := range st.OutputURLs {
			st.OutputURLs[i] = h.signer.Sign(u)
		}
	}
	writeJSON(w, http.StatusOK, st)
}

// Status 前端轮询:processing / succeeded(带签名 outputUrls) / failed(带 error)。
func (h *CanvasGenHandler) Status(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "taskID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}
	st, err := h.svc.GetStatus(r.Context(), userID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.signer != nil {
		for i, u := range st.OutputURLs {
			st.OutputURLs[i] = h.signer.Sign(u)
		}
	}
	writeJSON(w, http.StatusOK, st)
}
