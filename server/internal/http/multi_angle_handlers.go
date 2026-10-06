package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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

// AdminGet GET /api/v1/admin/canvas/multi-angle —— 回显全量（含 prompt/template），admin-only。
func (h *MultiAngleHandler) AdminGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.load())
}

// AdminPut PUT /api/v1/admin/canvas/multi-angle —— 整体覆盖。
// 只校验会让运行时出错的硬伤(key 冲突/缺 name/zoom 非法档)；prompt 允许为空
// (纯角度预设，如正面俯拍)——与绘图预设「prompt 必填」不同，这里角度本身就是内容。
func (h *MultiAngleHandler) AdminPut(w http.ResponseWriter, r *http.Request) {
	var body canvasgen.MultiAngleConfig
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Models == nil {
		body.Models = []string{}
	}
	if body.Presets == nil {
		body.Presets = []canvasgen.MultiAnglePreset{}
	}
	for i, m := range body.Models {
		body.Models[i] = strings.TrimSpace(m)
		if body.Models[i] == "" {
			writeError(w, http.StatusBadRequest, "模型 ID 不能为空")
			return
		}
	}
	zoomOK := map[string]bool{}
	for _, z := range canvasgen.MultiAngleZoomKeys {
		zoomOK[z] = true
	}
	seen := map[string]bool{}
	for i, p := range body.Presets {
		if !genImagePresetKeyRe.MatchString(p.Key) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条：key 必须是 1-64 位小写字母/数字/下划线/连字符", i+1))
			return
		}
		if p.Key == canvasgen.MultiAnglePresetKey {
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
		if p.Rotation < 0 || p.Rotation > 359 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条（%s）：rotation 必须在 0-359", i+1, p.Key))
			return
		}
		if p.Tilt < -90 || p.Tilt > 90 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条（%s）：tilt 必须在 -90~90", i+1, p.Key))
			return
		}
		if !zoomOK[p.Zoom] {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条（%s）：zoom 必须是 %s 之一", i+1, p.Key, strings.Join(canvasgen.MultiAngleZoomKeys, "/")))
			return
		}
	}
	blob, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.settings.Set(canvasgen.SettingCanvasMultiAngle, string(blob)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, body)
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
