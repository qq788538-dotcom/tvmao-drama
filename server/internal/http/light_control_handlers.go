package http

import (
	"net/http"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasgen"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/settings"
)

// LightControlHandler 「绘图打光设置」：后台配全量(含保密 prompt/template)，
// 用户端拿参数与参考图 URL（点预设表单跳位/带图要用）——机制与 MultiAngleHandler
// 逐项对齐，保密边界只有 prompt 与 template。
type LightControlHandler struct {
	settings settings.Repository
}

func NewLightControlHandler(repo settings.Repository) *LightControlHandler {
	return &LightControlHandler{settings: repo}
}

func (h *LightControlHandler) load() canvasgen.LightControlConfig {
	cfg, _ := canvasgen.LoadLightControlConfig(h.settings.Get)
	if cfg.Models == nil {
		cfg.Models = []string{}
	}
	if cfg.Presets == nil {
		cfg.Presets = []canvasgen.LightControlPreset{}
	}
	return cfg
}

// UserGet GET /api/v1/canvas/light-control —— 登录用户可读，供画布渲染打光面板。
// 脱敏：下发 models 与预设的参数字段(点预设表单跳位/带参考图要用)；
// prompt 与 template 是保密内容，永不出服务端(同绘图预设/多角度的白名单铁律)。
func (h *LightControlHandler) UserGet(w http.ResponseWriter, r *http.Request) {
	cfg := h.load()
	presets := make([]map[string]any, 0, len(cfg.Presets))
	for _, p := range cfg.Presets {
		item := map[string]any{
			"key":            p.Key,
			"name":           p.Name,
			"thumbnail":      p.Thumbnail,
			"direction":      p.Direction,
			"lightColor":     p.LightColor,
			"referenceImage": p.ReferenceImage,
		}
		// 指针字段显式判空再放行：直接塞 nil 指针 json 会出 null，前端还得判两种空。
		if p.Brightness != nil {
			item["brightness"] = *p.Brightness
		}
		presets = append(presets, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": cfg.Models, "presets": presets})
}
