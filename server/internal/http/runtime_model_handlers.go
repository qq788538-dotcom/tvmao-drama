package http

import (
	nethttp "net/http"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

type runtimeModelLister interface {
	List(onlyVisible bool) ([]models.ModelConfig, error)
}

// RuntimeModelHandler：画布取可用模型列表。估价接口保留路由但恒返回 0（桌面端没有积分与估价；
// 前端仍有调用方，返回 0 比 404 平滑）。单独成文件，与设置页的模型中心 handler 分开。
type RuntimeModelHandler struct {
	repo runtimeModelLister
}

func NewRuntimeModelHandler(repo runtimeModelLister) *RuntimeModelHandler {
	return &RuntimeModelHandler{repo: repo}
}

// List GET /api/v1/models → {data:[ModelConfig]}（只含启用且未隐藏的模型）
func (h *RuntimeModelHandler) List(w nethttp.ResponseWriter, r *nethttp.Request) {
	list, err := h.repo.List(true)
	if err != nil {
		writeError(w, nethttp.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []models.ModelConfig{}
	}
	for i := range list {
		list[i].AdminNote = ""
		list[i].PriceNote = "" // 不下发价格说明
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"data": list})
}

// Estimate POST /api/v1/models/estimate → {credits:0}
func (h *RuntimeModelHandler) Estimate(w nethttp.ResponseWriter, r *nethttp.Request) {
	writeJSON(w, nethttp.StatusOK, map[string]any{"credits": 0})
}

// EstimateBatch POST /api/v1/models/estimate-batch → {total:0, items:[0…]}（条数与请求一致）
func (h *RuntimeModelHandler) EstimateBatch(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, nethttp.StatusBadRequest, "invalid body")
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"total": 0, "items": make([]int64, len(body.Items))})
}
