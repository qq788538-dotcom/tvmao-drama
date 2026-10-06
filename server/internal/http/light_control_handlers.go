package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
	"github.com/james/huabu_studio/apps/api/internal/settings"
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

var lightColorHexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

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

// AdminGet GET /api/v1/admin/canvas/light-control —— 回显全量（含 prompt/template），admin-only。
func (h *LightControlHandler) AdminGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.load())
}

// AdminPut PUT /api/v1/admin/canvas/light-control —— 整体覆盖。
// 只校验会让运行时出错的硬伤(key 冲突/缺 name/方向非法/亮度越界/颜色非 hex)；
// 其余字段全部允许为空——打光预设本来就是「参数/保密词/参考图」的自由组合。
func (h *LightControlHandler) AdminPut(w http.ResponseWriter, r *http.Request) {
	var body canvasgen.LightControlConfig
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Models == nil {
		body.Models = []string{}
	}
	if body.Presets == nil {
		body.Presets = []canvasgen.LightControlPreset{}
	}
	for i, m := range body.Models {
		body.Models[i] = strings.TrimSpace(m)
		if body.Models[i] == "" {
			writeError(w, http.StatusBadRequest, "模型 ID 不能为空")
			return
		}
	}
	dirOK := map[string]bool{}
	for _, d := range canvasgen.LightDirectionKeys {
		dirOK[d] = true
	}
	seen := map[string]bool{}
	for i, p := range body.Presets {
		if !genImagePresetKeyRe.MatchString(p.Key) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条：key 必须是 1-64 位小写字母/数字/下划线/连字符", i+1))
			return
		}
		if p.Key == canvasgen.LightControlPresetKey {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条：key 是系统保留字", i+1))
			return
		}
		if seen[p.Key] {
			writeError(w, http.StatusBadRequest, "key 重复："+p.Key)
			return
		}
		seen[p.Key] = true
		if strings.TrimSpace(p.Name) == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条（%s）：name 不能为空", i+1, p.Key))
			return
		}
		if p.Direction != "" && !dirOK[p.Direction] {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条（%s）：direction 不是合法方向枚举", i+1, p.Key))
			return
		}
		if p.Brightness != nil && (*p.Brightness < 0 || *p.Brightness > 100) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条（%s）：brightness 必须在 0-100", i+1, p.Key))
			return
		}
		if p.LightColor != "" && !lightColorHexRe.MatchString(p.LightColor) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条（%s）：lightColor 必须是 #rrggbb", i+1, p.Key))
			return
		}
	}
	blob, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.settings.Set(canvasgen.SettingCanvasLightControl, string(blob)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, body)
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
