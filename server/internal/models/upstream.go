package models

import (
	"errors"
	"strings"
)

// 全局上游配置所在的 settings key（spec M2/M3）。
const (
	SettingBaseDomain     = "base_domain"
	SettingUpstreamAPIKey = "upstream_api_key"
)

// UpstreamSettings 是 ResolveUpstream 读全局配置用的最小接口（settings.Repository 直接满足）。
type UpstreamSettings interface {
	Get(key string) (string, error)
}

// HasOwnUpstream 报告模型是否单独配置了上游（地址和 Key 都非空才算）。
func (m *ModelConfig) HasOwnUpstream() bool {
	return m != nil && strings.TrimSpace(m.UpstreamBaseURL) != "" && strings.TrimSpace(m.UpstreamAPIKey) != ""
}

// ResolveUpstream 是调上游时取地址与 Key 的唯一入口（2026-10-07 每模型独立上游）。
//
// 模型两项都配了就用模型自己的；否则（含 m == nil，即调用与具体模型无关）读全局
// settings.base_domain + settings.upstream_api_key。返回值可能为空串，由调用方按各自
// 场景报“未配置”；err 只表示读全局配置失败。
func ResolveUpstream(m *ModelConfig, global UpstreamSettings) (baseURL, apiKey string, err error) {
	if m.HasOwnUpstream() {
		return strings.TrimSpace(m.UpstreamBaseURL), strings.TrimSpace(m.UpstreamAPIKey), nil
	}
	if global == nil {
		return "", "", errors.New("全局上游配置未接入")
	}
	if baseURL, err = global.Get(SettingBaseDomain); err != nil {
		return "", "", err
	}
	if apiKey, err = global.Get(SettingUpstreamAPIKey); err != nil {
		return "", "", err
	}
	return baseURL, apiKey, nil
}

// GlobalWithKey 包一层全局配置：upstream_api_key 改由 key() 提供，其余 key 照常读 s。
// 给沿用参考仓“按用户解析 Key”接口（UpstreamKeyLookup 等）的调用方接入 ResolveUpstream。
func GlobalWithKey(s UpstreamSettings, key func() (string, error)) UpstreamSettings {
	return globalWithKey{s: s, key: key}
}

type globalWithKey struct {
	s   UpstreamSettings
	key func() (string, error)
}

func (g globalWithKey) Get(k string) (string, error) {
	if k == SettingUpstreamAPIKey && g.key != nil {
		return g.key()
	}
	if g.s == nil {
		return "", errors.New("全局上游配置未接入")
	}
	return g.s.Get(k)
}

// ErrUpstreamUnpaired：独立上游的地址与 Key 只给了一项。
var ErrUpstreamUnpaired = errors.New("单独配置上游时，地址和 API Key 都必须填写")

// SetUpstream 写入（两项都非空）或清空（两项都为空）模型的独立上游。不经 builtin 的
// user_overrides 机制——这是本机字段，内置数据升级不会碰它。
func (r *SQLiteRepository) SetUpstream(id int64, baseURL, apiKey string) error {
	if (baseURL == "") != (apiKey == "") {
		return ErrUpstreamUnpaired
	}
	res, err := r.db.Exec(`UPDATE model_configs SET upstream_base_url = ?, upstream_api_key = ? WHERE id = ?`, baseURL, apiKey, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
