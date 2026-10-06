package http

import (
	"encoding/json"
	"errors"
	"log"
	nethttp "net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/canvasscript"
)

// canvasScriptUserID 桌面端固定本地用户（P1a 迁移预置 users.id=1）。
// 合并 P1b 的本地身份中间件后可改用 userIDFromContext，行为不变。
const canvasScriptUserID int64 = 1

// CanvasScriptHandler 脚本节点“剧本生成分镜”的用户侧接口：风格列表、提交、轮询。
// 后台“脚本设置”（读写 canvas_script_config）归 P4，直接调用 Service.LoadConfig / SaveConfig。
type CanvasScriptHandler struct {
	svc *canvasscript.Service
}

func NewCanvasScriptHandler(svc *canvasscript.Service) *CanvasScriptHandler {
	return &CanvasScriptHandler{svc: svc}
}

func canvasScriptError(w nethttp.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func (h *CanvasScriptHandler) Styles(w nethttp.ResponseWriter, _ *nethttp.Request) {
	styles, cands, err := h.svc.EnabledStyles(nil)
	if err != nil {
		log.Printf("canvasscript: 读取风格列表失败: %v", err)
		canvasScriptError(w, nethttp.StatusInternalServerError, "读取风格列表失败，请稍后重试")
		return
	}
	type styleOut struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	out := make([]styleOut, 0, len(styles))
	for _, s := range styles {
		// skillName 是内部映射，不下发。
		out = append(out, styleOut{Name: s.Name, Version: s.Version})
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"styles": out, "modelCandidates": cands})
}

type canvasScriptSubmitBody struct {
	ProjectID   int64  `json:"projectId"`
	NodeID      string `json:"nodeId"`
	Style       string `json:"style"`
	DurationSec int    `json:"durationSec"`
	Script      string `json:"script"`
	Model       string `json:"model"`
}

func (h *CanvasScriptHandler) Submit(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body canvasScriptSubmitBody
	r.Body = nethttp.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		canvasScriptError(w, nethttp.StatusBadRequest, "invalid body")
		return
	}
	if body.ProjectID == 0 || body.NodeID == "" || body.Style == "" || body.Model == "" {
		canvasScriptError(w, nethttp.StatusBadRequest, "projectId/nodeId/style/model required")
		return
	}
	taskID, err := h.svc.Submit(r.Context(), canvasScriptUserID, body.ProjectID, body.NodeID, body.Style, body.Script, body.DurationSec, body.Model, nil)
	if err != nil {
		var ue *canvasscript.UserError
		if errors.As(err, &ue) {
			canvasScriptError(w, nethttp.StatusBadRequest, ue.Msg)
			return
		}
		log.Printf("canvasscript: 提交失败: %v", err)
		canvasScriptError(w, nethttp.StatusInternalServerError, "提交失败，请稍后重试")
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"taskId": taskID, "status": canvasscript.StatusPending})
}

func (h *CanvasScriptHandler) Status(w nethttp.ResponseWriter, r *nethttp.Request) {
	taskID, err := strconv.ParseInt(chi.URLParam(r, "taskID"), 10, 64)
	if err != nil {
		canvasScriptError(w, nethttp.StatusBadRequest, "invalid taskID")
		return
	}
	t, err := h.svc.Get(r.Context(), canvasScriptUserID, taskID)
	if err != nil {
		if errors.Is(err, canvasscript.ErrTaskNotFound) {
			canvasScriptError(w, nethttp.StatusNotFound, "任务不存在")
			return
		}
		log.Printf("canvasscript: 查询任务失败: %v", err)
		canvasScriptError(w, nethttp.StatusInternalServerError, "查询任务失败，请稍后重试")
		return
	}
	out := map[string]any{"status": t.Status, "nodeId": t.NodeID, "durationSec": t.DurationSec}
	if t.Status == canvasscript.StatusSucceeded {
		out["result"] = t.Result
	}
	if t.Status == canvasscript.StatusFailed {
		// 落库的 error 已是面向用户的固定文案；详情只在日志里。
		out["error"] = t.Error
	}
	writeJSON(w, nethttp.StatusOK, out)
}
