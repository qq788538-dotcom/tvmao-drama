package http

import (
	"net/http"

	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
	"github.com/james/huabu_studio/apps/api/internal/settings"
)

// MultiAngleHandler 「绘图多角度设置」：后台配全量(含保密 prompt/template)，
// 用户端只拿角度数值与名字——机制与 GenImagePresetHandler 逐项对齐。
type MultiAngleHandler struct {
	settings settings.Repository
}

func NewMultiAngleHandler(repo settings.Repository) *MultiAngleHandler {
	return &MultiAngleHandler{settings: repo}
}

func (h *MultiAngleHandler) load() canvasgen.MultiAngleConfig {
	cfg, _ := canvasgen.LoadMultiAngleConfig(h.settings.Get)
	if cfg.Models == nil {
		cfg.Models = []string{}
	}
	if cfg.Presets == nil {
		cfg.Presets = []canvasgen.MultiAnglePreset{}
	}
	return cfg
}

// UserGet GET /api/v1/canvas/multi-angle —— 登录用户可读，供画布渲染多角度编辑器。
// 脱敏：只下发 models 与预设的 {key,name,rotation,tilt,zoom}(点预设时滑杆跳位要用)；
// prompt 与 template 是保密内容，永不出服务端(同绘图预设的白名单铁律)。
func (h *MultiAngleHandler) UserGet(w http.ResponseWriter, r *http.Request) {
	cfg := h.load()
	presets := make([]map[string]any, 0, len(cfg.Presets))
	for _, p := range cfg.Presets {
		presets = append(presets, map[string]any{
			"key":      p.Key,
			"name":     p.Name,
			"rotation": p.Rotation,
			"tilt":     p.Tilt,
			"zoom":     p.Zoom,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": cfg.Models, "presets": presets})
}
