package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
)

type GenerationHandler struct {
	service *generation.Service
	signer  mediaRefSigner
}

func NewGenerationHandler(service *generation.Service, signer mediaRefSigner) *GenerationHandler {
	return &GenerationHandler{service: service, signer: signer}
}

// signResult 把 task output / outputUrls 里的每个字符串替换成一个新鲜的
// 签名 URL。即便 signer 为 nil 也可以安全调用。
func (h *GenerationHandler) signTask(task *generation.Task) {
	if task == nil || h.signer == nil {
		return
	}
	task.Input = h.signer.SignJSON(task.Input)
	task.Output = h.signer.SignJSON(task.Output)
}

func (h *GenerationHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	filter := generation.ListFilter{
		UserID:  userID,
		Status:  q.Get("status"),
		ModelID: q.Get("modelId"),
		Search:  q.Get("search"),
		Limit:   limit,
		Offset:  offset,
	}
	// kind 白名单校验：列值只有三种,非法值当没传(空过滤)而不是报错——
	// 与 status/modelId 的宽松风格一致。
	if k := q.Get("kind"); k == "image" || k == "video" || k == "audio" {
		filter.Kind = k
	}
	if pid := q.Get("projectId"); pid != "" {
		if v, err := strconv.ParseInt(pid, 10, 64); err == nil {
			filter.ProjectID = &v
		}
	}
	tasks, total, err := h.service.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range tasks {
		h.signTask(&tasks[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": tasks, "total": total})
}

func (h *GenerationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "taskID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}
	if err := h.service.Delete(r.Context(), userID, id); err != nil {
		if errors.Is(err, generation.ErrNotFound) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		if errors.Is(err, generation.ErrTaskNotTerminal) {
			// 删除只清历史,不是取消通道:非终态任务持有冻结,等它收口后再删。
			writeError(w, http.StatusConflict, "任务仍在进行中，请等待其结束后再删除")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
