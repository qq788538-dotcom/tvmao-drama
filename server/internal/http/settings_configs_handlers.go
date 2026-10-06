package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

func init() { settingsRouteRegistrars = append(settingsRouteRegistrars, registerConfigSettings) }

func registerConfigSettings(r chi.Router, d SettingsCenterDeps) {
	if d.Settings == nil || d.SettingEdit == nil || d.Models == nil {
		return
	}
	h := &configSettingsHandler{d: d}
	r.Get("/configs/{key}", h.get)
	r.Put("/configs/{key}", h.put)
	r.Post("/configs/{key}/restore-default", h.restore)
}

type configSettingsHandler struct{ d SettingsCenterDeps }

type configView struct {
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
	Modified bool            `json:"modified"`
}

func configKeyFrom(r *nethttp.Request) (string, bool) {
	k := chi.URLParam(r, "key")
	for _, allowed := range settingsConfigKeys {
		if k == allowed {
			return k, true
		}
	}
	return "", false
}

func (h *configSettingsHandler) view(r *nethttp.Request, key string) (configView, error) {
	raw, err := h.d.Settings.Get(key)
	if err != nil {
		return configView{}, err
	}
	mods, err := h.d.SettingEdit.ModifiedSettings(r.Context())
	if err != nil {
		return configView{}, err
	}
	v := configView{Key: key, Value: json.RawMessage("null")}
	if strings.TrimSpace(raw) != "" {
		v.Value = json.RawMessage(raw)
	}
	for _, m := range mods {
		if m == key {
			v.Modified = true
		}
	}
	return v, nil
}

func (h *configSettingsHandler) writeView(w nethttp.ResponseWriter, r *nethttp.Request, key string) {
	v, err := h.view(r, key)
	if err != nil {
		scWriteError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, v)
}

func (h *configSettingsHandler) get(w nethttp.ResponseWriter, r *nethttp.Request) {
	key, ok := configKeyFrom(r)
	if !ok {
		scWriteError(w, ErrSettingsNotFound)
		return
	}
	h.writeView(w, r, key)
}

func (h *configSettingsHandler) put(w nethttp.ResponseWriter, r *nethttp.Request) {
	key, ok := configKeyFrom(r)
	if !ok {
		scWriteError(w, ErrSettingsNotFound)
		return
	}
	var body struct {
		Value json.RawMessage `json:"value"`
	}
	if err := scDecodeStrict(w, r, &body); err != nil {
		scWriteError(w, err)
		return
	}
	trimmed := bytes.TrimSpace(body.Value)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		scWriteError(w, scValidation("value", "配置必须是 JSON 对象"))
		return
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		scWriteError(w, scValidation("value", "配置不是合法的 JSON"))
		return
	}
	if err := validateConfigValue(key, compact.Bytes(), h.d.Models.GetByModelID); err != nil {
		scWriteError(w, err)
		return
	}
	if err := h.d.SettingEdit.UpdateSetting(r.Context(), key, compact.String()); err != nil {
		scWriteError(w, err)
		return
	}
	h.writeView(w, r, key)
}

func (h *configSettingsHandler) restore(w nethttp.ResponseWriter, r *nethttp.Request) {
	key, ok := configKeyFrom(r)
	if !ok {
		scWriteError(w, ErrSettingsNotFound)
		return
	}
	if err := h.d.SettingEdit.RestoreSettingDefault(r.Context(), key); err != nil {
		scWriteError(w, err)
		return
	}
	h.writeView(w, r, key)
}

var modelTypeLabels = map[string]string{"image": "图片", "video": "视频", "text": "文本", "audio": "音频"}

// validateConfigValue 校验一项画布配置里结构不变时仍可能出错的取值。
func validateConfigValue(key string, raw []byte, lookup func(string) (*models.ModelConfig, error)) error {
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return scValidation("value", "配置不是合法的 JSON")
	}
	// models 数组里的空字符串 extractModelRefs 会跳过，单独拦下（参考仓同样拒绝空模型 ID）。
	if arr, ok := v["models"].([]any); ok {
		for i, m := range arr {
			if s, _ := m.(string); strings.TrimSpace(s) == "" {
				return scValidation(fmt.Sprintf("models[%d]", i), "模型不能为空")
			}
		}
	}
	refs, err := extractModelRefs(key, raw)
	if err != nil {
		return scValidation("value", "配置不是合法的 JSON")
	}
	for _, ref := range refs {
		m, err := lookup(ref.ModelID)
		if errors.Is(err, models.ErrNotFound) {
			return scValidation(ref.Path, "模型「"+ref.ModelID+"」不存在")
		}
		if err != nil {
			return err
		}
		if m.Type != ref.WantType {
			return scValidation(ref.Path, fmt.Sprintf("「%s」不是%s模型", m.Name, modelTypeLabels[ref.WantType]))
		}
		if !m.Enabled {
			return scValidation(ref.Path, "模型「"+m.Name+"」已停用，请先在模型中心启用")
		}
	}
	switch key {
	case "gen_image_prompt_presets":
		presets, _ := v["presets"].([]any)
		for i, p := range presets {
			obj, _ := p.(map[string]any)
			val, _ := obj["value"].(map[string]any)
			name, _ := val["name"].(string)
			if prompt, _ := val["prompt"].(string); strings.TrimSpace(prompt) == "" {
				return scValidation(fmt.Sprintf("presets[%d].value.prompt", i), "预设「"+name+"」的提示词不能为空")
			}
		}
	case "canvas_multi_angle":
		return checkTemplate(v, "{angle}")
	case "canvas_light_control":
		return checkTemplate(v, "{light}")
	case "canvas_script_config":
		if err := checkNumber(v, "concurrency", "并发数", 1, 20); err != nil {
			return err
		}
		return checkNumber(v, "maxRetries", "失败重试次数", 0, 10)
	}
	return nil
}

func checkTemplate(v map[string]any, placeholder string) error {
	t, _ := v["template"].(string)
	if strings.TrimSpace(t) != "" && !strings.Contains(t, placeholder) {
		return scValidation("template", "拼接模板必须保留 "+placeholder+"（留空则使用内置模板）")
	}
	return nil
}

func checkNumber(v map[string]any, field, label string, lo, hi float64) error {
	raw, ok := v[field]
	if !ok {
		return nil
	}
	n, isNum := raw.(float64)
	if !isNum || n != float64(int(n)) || n < lo || n > hi {
		return scValidation(field, fmt.Sprintf("%s必须是 %d 到 %d 的整数", label, int(lo), int(hi)))
	}
	return nil
}
