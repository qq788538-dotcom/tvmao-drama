package models

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// 全局上游配置所在的 settings key。
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

// ResolveUpstream 是调上游时取地址与 Key 的唯一入口（支持每模型独立上游）。
//
// 模型两项都配了就用模型自己的；否则（含 m == nil，即调用与具体模型无关）读全局
// settings.base_domain + settings.upstream_api_key。返回值可能为空串，由调用方按各自
// 场景报“未配置”；err 只表示读全局配置失败。
func ResolveUpstream(m *ModelConfig, global UpstreamSettings) (baseURL, apiKey string, err error) {
	if m.HasOwnUpstream() {
		return NormalizeUpstreamBaseURL(m.UpstreamBaseURL), strings.TrimSpace(m.UpstreamAPIKey), nil
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
	return NormalizeUpstreamBaseURL(baseURL), apiKey, nil
}

// NormalizeUpstreamBaseURL 去掉地址首尾空白、末尾的 "/" 和一段 "/v1"（不区分大小写）。
//
// 上游地址约定填 newapi 根地址：画布生成拼 base + base_path（种子里的 base_path 自带 /v1），
// Agent 与剧本生成在 base 后补 /v1。用户常直接粘贴 "https://x/v1"，不去掉就会在画布生成
// 路径拼出 /v1/v1。保存时规范化一次，解析出口（ResolveUpstream）再兜一次存量旧值。
func NormalizeUpstreamBaseURL(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if n := len(s); n >= 3 && strings.EqualFold(s[n-3:], "/v1") {
		s = strings.TrimRight(s[:n-3], "/")
	}
	return s
}

// UpstreamFingerprint 标识"哪个上游账号"：规范化地址 + "#" + Key 的 sha256 前 16 个十六进制字符。
// 不含 Key 明文，可以落库（生成任务记录提交时的上游，重启续轮询时比对）。
func UpstreamFingerprint(baseURL, apiKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(apiKey)))
	return NormalizeUpstreamBaseURL(baseURL) + "#" + hex.EncodeToString(sum[:])[:16]
}

// GlobalWithKey 包一层全局配置：upstream_api_key 改由 key() 提供，其余 key 照常读 s。
// 给沿用“按用户解析 Key”接口（UpstreamKeyLookup 等）的调用方接入 ResolveUpstream。
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
