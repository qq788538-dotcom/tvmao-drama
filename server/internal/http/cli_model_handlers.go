package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/cliapi"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

// cliModelRepo 是 CLI 模型接口需要的 models.Repository 子集。
type cliModelRepo interface {
	List(onlyVisible bool) ([]models.ModelConfig, error)
	GetByModelID(modelID string) (*models.ModelConfig, error)
}

// CLIModelHandler 暴露 CLI 可见的模型目录（桌面端无计费，不含费用估算）。
//
// 与公开的 /api/v1/models 是两套响应：那条直接回内部 ModelConfig（网页需要
// uiSchema 渲染下拉框），这条回归一化后的公共契约（Agent 需要 JSON Schema）。
type CLIModelHandler struct {
	repo cliModelRepo
}

func NewCLIModelHandler(repo cliModelRepo) *CLIModelHandler {
	return &CLIModelHandler{repo: repo}
}

// visible 取 CLI 可见模型：enabled && !hidden，与网页 /models 同一口径。
func (h *CLIModelHandler) visible() ([]models.ModelConfig, error) {
	list, err := h.repo.List(true)
	if err != nil {
		return nil, cliapi.New(cliapi.CodeInternal, "读取模型列表失败").WithCause(err)
	}
	return list, nil
}

// List GET /cli/models?type=image|video&q=keyword
func (h *CLIModelHandler) List(w http.ResponseWriter, r *http.Request) {
	list, err := h.visible()
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	filtered := cliapi.FilterModels(list, r.URL.Query().Get("type"), r.URL.Query().Get("q"))
	out := make([]cliapi.Model, 0, len(filtered))
	for _, m := range filtered {
		out = append(out, cliapi.ToModel(m))
	}
	writeCLIJSON(w, http.StatusOK, map[string]any{"data": out})
}

// Get GET /cli/models/{modelID} —— 支持 ID 或精确名称，歧义报错不猜。
func (h *CLIModelHandler) Get(w http.ResponseWriter, r *http.Request) {
	list, err := h.visible()
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	m, err := cliapi.ResolveModelRef(list, chi.URLParam(r, "modelID"))
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, cliapi.ToModel(m))
}
