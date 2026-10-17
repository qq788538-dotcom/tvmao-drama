package http

import (
	"net/http"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasgen"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/settings"
)

// 绘图提示词预设：整块存 settings 一个 KV（先例 cli_release / canvas_postprocess_config）。
// 预设正文（value.prompt）是运营资产，对前端保密——用户端点白名单剥字段后才下发，
// 提交生成只传 key，key → 正文的替换在服务端执行层完成。

// genImagePresetPublicFields 是用户端点下发 value 时的字段白名单。
// 用白名单而非「删掉 prompt」的黑名单：将来 value 里再加敏感字段，黑名单忘了删就
// 泄露；白名单忘了加只是前端少个展示字段，错的方向是安全的。
var genImagePresetPublicFields = []string{
	"name", "category", "icon", "description", "placeholder", "minImages", "maxImages", "sort",
}

type GenImagePresetHandler struct {
	settings settings.Repository
}

func NewGenImagePresetHandler(repo settings.Repository) *GenImagePresetHandler {
	return &GenImagePresetHandler{settings: repo}
}

// load 读配置；未设置/损坏 → 空配置（不 500：后台首次进入要能渲染，用户端菜单隐藏即可）。
func (h *GenImagePresetHandler) load() canvasgen.GenImagePresetsConfig {
	cfg, _ := canvasgen.LoadGenImagePresetsConfig(h.settings.Get)
	if cfg.Models == nil {
		cfg.Models = []string{}
	}
	if cfg.Presets == nil {
		cfg.Presets = []canvasgen.GenImagePresetEntry{}
	}
	return cfg
}

// UserGet GET /api/v1/canvas/gen-image-presets —— 登录用户可读，供绘图节点渲染预设菜单。
// value 走白名单剥离后下发：prompt 正文绝不出现在这个响应里。
func (h *GenImagePresetHandler) UserGet(w http.ResponseWriter, r *http.Request) {
	cfg := h.load()
	presets := make([]map[string]any, 0, len(cfg.Presets))
	for _, p := range cfg.Presets {
		v := map[string]any{}
		for _, f := range genImagePresetPublicFields {
			if raw, ok := p.Value[f]; ok {
				v[f] = raw
			}
		}
		// params 不能走平铺白名单：维度/档位的名字要下发（前端渲染参数面板），
		// 但每个档位的 prompt 片段与预设正文同级保密——重建一份只含展示字段的结构。
		if params := p.Params(); len(params) > 0 {
			outParams := make([]map[string]any, 0, len(params))
			for _, dim := range params {
				opts := make([]map[string]any, 0, len(dim.Options))
				for _, o := range dim.Options {
					m := map[string]any{"key": o.Key, "name": o.Name}
					if o.Default {
						m["default"] = true
					}
					opts = append(opts, m)
				}
				outParams = append(outParams, map[string]any{"key": dim.Key, "name": dim.Name, "options": opts})
			}
			v["params"] = outParams
		}
		presets = append(presets, map[string]any{"key": p.Key, "value": v})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": cfg.Models, "presets": presets})
}
