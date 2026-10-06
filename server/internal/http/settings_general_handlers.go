package http

import (
	nethttp "net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/datadir"
)

const (
	settingBaseDomain     = "base_domain"
	settingUpstreamAPIKey = "upstream_api_key"
)

func init() {
	settingsRouteRegistrars = append(settingsRouteRegistrars, registerGeneralSettings, registerDataDirCheck)
}

func registerGeneralSettings(r chi.Router, d SettingsCenterDeps) {
	if d.Settings == nil || d.SettingEdit == nil {
		return
	}
	h := &generalSettingsHandler{d: d}
	r.Get("/general", h.get)
	r.Put("/upstream", h.putUpstream)
	r.Post("/upstream/base-domain/restore-default", h.restoreBaseDomain)
}

func registerDataDirCheck(r chi.Router, d SettingsCenterDeps) {
	if d.DataDir == "" {
		return
	}
	r.Post("/data-dir/check", func(w nethttp.ResponseWriter, req *nethttp.Request) {
		var body struct {
			Path string `json:"path"`
		}
		if err := scDecodeStrict(w, req, &body); err != nil {
			scWriteError(w, err)
			return
		}
		cleaned, err := datadir.ValidateTarget(d.DataDir, body.Path)
		if err != nil {
			scWriteError(w, scValidation("path", err.Error()))
			return
		}
		writeJSON(w, nethttp.StatusOK, map[string]any{"path": cleaned})
	})
}

type generalSettingsHandler struct{ d SettingsCenterDeps }

type generalSettingsView struct {
	Version            string `json:"version"`
	DataDir            string `json:"dataDir"`
	BaseDomain         string `json:"baseDomain"`
	BaseDomainModified bool   `json:"baseDomainModified"`
	APIKeySet          bool   `json:"apiKeySet"`
	APIKeyMasked       string `json:"apiKeyMasked"`
}

func (h *generalSettingsHandler) view(r *nethttp.Request) (generalSettingsView, error) {
	bd, err := h.d.Settings.Get(settingBaseDomain)
	if err != nil {
		return generalSettingsView{}, err
	}
	key, err := h.d.Settings.Get(settingUpstreamAPIKey)
	if err != nil {
		return generalSettingsView{}, err
	}
	mods, err := h.d.SettingEdit.ModifiedSettings(r.Context())
	if err != nil {
		return generalSettingsView{}, err
	}
	modified := false
	for _, k := range mods {
		if k == settingBaseDomain {
			modified = true
		}
	}
	return generalSettingsView{
		Version: h.d.Version, DataDir: h.d.DataDir,
		BaseDomain: bd, BaseDomainModified: modified,
		APIKeySet: key != "", APIKeyMasked: maskAPIKey(key),
	}, nil
}

func (h *generalSettingsHandler) writeView(w nethttp.ResponseWriter, r *nethttp.Request) {
	v, err := h.view(r)
	if err != nil {
		scWriteError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, v)
}

func (h *generalSettingsHandler) get(w nethttp.ResponseWriter, r *nethttp.Request) { h.writeView(w, r) }

func (h *generalSettingsHandler) putUpstream(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body struct {
		BaseDomain  *string `json:"baseDomain"`
		APIKey      *string `json:"apiKey"`
		ClearAPIKey bool    `json:"clearApiKey"`
	}
	if err := scDecodeStrict(w, r, &body); err != nil {
		scWriteError(w, err)
		return
	}
	if body.BaseDomain == nil && body.APIKey == nil && !body.ClearAPIKey {
		scWriteError(w, scValidation("", "没有要修改的字段"))
		return
	}
	if body.APIKey != nil && body.ClearAPIKey {
		scWriteError(w, scValidation("apiKey", "不能同时设置和清除 API Key"))
		return
	}
	// 先全部校验，再写入：避免地址写进去了、Key 却因格式错误被拒的半成功。
	var newBase, newKey string
	var err error
	if body.BaseDomain != nil {
		if newBase, err = normalizeBaseDomain(*body.BaseDomain); err != nil {
			scWriteError(w, err)
			return
		}
	}
	if body.APIKey != nil {
		if newKey, err = normalizeAPIKey(*body.APIKey); err != nil {
			scWriteError(w, err)
			return
		}
	}
	if body.BaseDomain != nil {
		cur, err := h.d.Settings.Get(settingBaseDomain)
		if err != nil {
			scWriteError(w, err)
			return
		}
		if strings.TrimRight(cur, "/") != newBase {
			if err := h.d.SettingEdit.UpdateSetting(r.Context(), settingBaseDomain, newBase); err != nil {
				scWriteError(w, err)
				return
			}
		}
	}
	// API Key 没有内置默认值、不参与 M11 合并，直接写 settings（spec M3）。
	if body.APIKey != nil || body.ClearAPIKey {
		if err := h.d.Settings.Set(settingUpstreamAPIKey, newKey); err != nil {
			scWriteError(w, err)
			return
		}
	}
	h.writeView(w, r)
}

func (h *generalSettingsHandler) restoreBaseDomain(w nethttp.ResponseWriter, r *nethttp.Request) {
	if err := h.d.SettingEdit.RestoreSettingDefault(r.Context(), settingBaseDomain); err != nil {
		scWriteError(w, err)
		return
	}
	h.writeView(w, r)
}

// maskAPIKey 返回固定格式的掩码：最多露后 4 位，不露前几位；不足 12 位全部遮住。
// 以 "sk-" 开头的 Key 保留这个通用前缀（spec 格式 sk-••••••••末4位）。Key 明文永不出 sidecar。
func maskAPIKey(k string) string {
	if k == "" {
		return ""
	}
	const dots = "••••••••"
	r := []rune(k)
	if len(r) < 12 {
		return dots
	}
	prefix := ""
	if strings.HasPrefix(k, "sk-") {
		prefix = "sk-"
	}
	return prefix + dots + string(r[len(r)-4:])
}

// normalizeBaseDomain 只接受 http/https 绝对地址，去掉末尾 "/"；允许路径前缀（反向代理场景）。
func normalizeBaseDomain(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", scValidation("baseDomain", "请填写上游地址")
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", scValidation("baseDomain", "上游地址必须是 http:// 或 https:// 开头的完整地址")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", scValidation("baseDomain", "上游地址不能包含账号、查询参数或 #")
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.Path, "/"), nil
}

// normalizeAPIKey 去首尾空白；只允许可见 ASCII，最长 512。
func normalizeAPIKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", scValidation("apiKey", "API Key 不能为空；要删除请点「清除」")
	}
	if len(s) > 512 {
		return "", scValidation("apiKey", "API Key 过长")
	}
	for _, c := range s {
		if c <= 0x20 || c >= 0x7f {
			return "", scValidation("apiKey", "API Key 只能包含可见的英文字符，不能有空格")
		}
	}
	return s, nil
}
