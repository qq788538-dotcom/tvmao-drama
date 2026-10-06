// Package seedance 是 Seedance 2.0「素材库」合规校验的薄 HTTP 客户端，只覆盖三个端点：
// 建组、建素材、查素材状态。
//
// 桌面端经 newapi 网关转发（spec X5）：地址 = settings.base_domain + 合规接口路径
// （settings.seedance_compliance_config），鉴权 = Bearer <upstream_api_key>。请求与响应的
// JSON 形状保持参考仓直连火山方舟素材库时的样子，newapi 侧按这个形状补接口（spec P7）。
//
// key 不存在 Client 上，而是每次调用作为参数传入；地址与路径每次请求现读——都存在
// settings 表里、设置页可随时改，避免进程内缓存过期值。
package seedance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SettingComplianceConfig 是合规接口配置在 settings 表里的 key（spec M4：每项配置存一个 JSON 值）。
const SettingComplianceConfig = "seedance_compliance_config"

// ErrNotConfigured：网关地址为空，或本次要调的接口路径为空。
var ErrNotConfigured = errors.New("合规接口未配置")

// Paths 是 newapi 上三个合规接口的路径，由用户在设置页填写。
type Paths struct {
	CreateAssetGroup string `json:"createAssetGroupPath"`
	CreateAsset      string `json:"createAssetPath"`
	GetAsset         string `json:"getAssetPath"`
}

// Complete 三个路径都填了才算配置完整。
func (p Paths) Complete() bool {
	return strings.TrimSpace(p.CreateAssetGroup) != "" &&
		strings.TrimSpace(p.CreateAsset) != "" &&
		strings.TrimSpace(p.GetAsset) != ""
}

// ComplianceConfig 是 settings.seedance_compliance_config 的整份内容。
type ComplianceConfig struct {
	Paths
	// Version 换账号或换 key 后 +1：素材组与已通过的素材都会在新版本下重建
	// （即参考仓的 settings.drama_seedance_version，桌面端并进这份配置）。<=0 视为 1。
	Version int `json:"version"`
}

// EffectiveVersion 返回生效的版本号（未设置为 1）。
func (c ComplianceConfig) EffectiveVersion() int {
	if c.Version > 0 {
		return c.Version
	}
	return 1
}

// LoadComplianceConfig 现读配置；未设置返回零值（= 未配置），JSON 损坏返回错误。
func LoadComplianceConfig(get func(key string) (string, error)) (ComplianceConfig, error) {
	raw, err := get(SettingComplianceConfig)
	if err != nil {
		return ComplianceConfig{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return ComplianceConfig{}, nil
	}
	var c ComplianceConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return ComplianceConfig{}, fmt.Errorf("合规接口配置损坏: %w", err)
	}
	return c, nil
}

// BaseURLProvider 现读网关地址（settings.base_domain）。
type BaseURLProvider func() string

// PathsProvider 现读三个接口路径。
type PathsProvider func() Paths

type Client struct {
	baseURL BaseURLProvider
	paths   PathsProvider
	http    *http.Client
}

// NewClient 用 30s 超时的 http.Client 构造。任一 provider 为 nil 或返回空值时，调用返回 ErrNotConfigured。
func NewClient(baseURL BaseURLProvider, paths PathsProvider) *Client {
	return &Client{baseURL: baseURL, paths: paths, http: &http.Client{Timeout: 30 * time.Second}}
}

// endpoint 拼出本次调用的完整地址。
func (c *Client) endpoint(pick func(Paths) string) (string, error) {
	base := ""
	if c.baseURL != nil {
		base = strings.TrimRight(strings.TrimSpace(c.baseURL()), "/")
	}
	var p Paths
	if c.paths != nil {
		p = c.paths()
	}
	path := strings.TrimSpace(pick(p))
	if base == "" || path == "" {
		return "", ErrNotConfigured
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path, nil
}

// post 发一个带 Bearer key 的 JSON POST，把响应 JSON 解到 out。
// 非 2xx 一律当错误，并把响应体塞进 error 方便排障。
func (c *Client) post(ctx context.Context, apiKey, name string, pick func(Paths) string, body, out any) error {
	url, err := c.endpoint(pick)
	if err != nil {
		return err
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if apiErr := errorFromResponse(respBody); apiErr != nil {
			apiErr.Detail = fmt.Sprintf("%s: status %d: %s", name, resp.StatusCode, apiErr.Detail)
			return apiErr
		}
		return fmt.Errorf("seedance %s: status %d: %s", name, resp.StatusCode, string(respBody))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("seedance %s: decode response: %w (body=%s)", name, err, string(respBody))
	}
	return nil
}

// APIError 是 seedance 侧返回的业务错误（HTTP 可能是 200，Code 藏在 data 里，
// 也可能是非 2xx 状态码）。单独成类型是为了让上层能按 Code 分类：
// InvalidParameter.* 这类参数错误重试必然同样失败，调用方据此停止重试。
type APIError struct {
	Code   string // 如 "InvalidParameter.AspectRatioTooSmall"；可能为空（未能解析出 Code）
	Detail string // 原始响应体或 Message，保留给日志/前端展示
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return "seedance " + e.Code + ": " + e.Detail
	}
	return "seedance: " + e.Detail
}

// errorFromResponse 尝试把响应体解析成 APIError（能取到 Code 才算数）。
func errorFromResponse(raw []byte) *APIError {
	code, ok := fieldFromResponse(raw, "Code")
	if !ok {
		return nil
	}
	detail, _ := fieldFromResponse(raw, "Message")
	if detail == "" {
		detail = string(raw)
	}
	return &APIError{Code: code, Detail: detail}
}

// fieldFromResponse 从响应里取一个字符串字段，兼容直返 {"Id":...} 与被包一层
// {"data":{"Id":...}} / {"Result":{...}} 等常见网关形态。取不到返回 false。
func fieldFromResponse(raw json.RawMessage, field string) (string, bool) {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return "", false
	}
	if v, ok := m[field].(string); ok && v != "" {
		return v, true
	}
	for _, wrap := range []string{"data", "Data", "result", "Result"} {
		if sub, ok := m[wrap].(map[string]any); ok {
			if v, ok := sub[field].(string); ok && v != "" {
				return v, true
			}
		}
	}
	return "", false
}

// CreateAssetGroup 建一个素材组，返回组 ID。
func (c *Client) CreateAssetGroup(ctx context.Context, sdKey, name, description string) (string, error) {
	var raw json.RawMessage
	if err := c.post(ctx, sdKey, "CreateAssetGroup", func(p Paths) string { return p.CreateAssetGroup }, map[string]any{
		"Name":        name,
		"Description": description,
	}, &raw); err != nil {
		return "", err
	}
	if id, ok := fieldFromResponse(raw, "Id"); ok {
		return id, nil
	}
	if apiErr := errorFromResponse(raw); apiErr != nil {
		return "", apiErr
	}
	return "", fmt.Errorf("seedance CreateAssetGroup: 响应无 Id: %s", string(raw))
}

// AssetType 取值。上游三类素材共用 CreateAsset/GetAsset，差别只在这一个字段。
const (
	AssetTypeImage = "Image"
	AssetTypeVideo = "Video"
	AssetTypeAudio = "Audio"
)

// CreateAsset 往组里加一个素材（异步），返回素材 ID。assetType 必传。
func (c *Client) CreateAsset(ctx context.Context, sdKey, groupID, url, name, assetType string) (string, error) {
	var raw json.RawMessage
	if err := c.post(ctx, sdKey, "CreateAsset", func(p Paths) string { return p.CreateAsset }, map[string]any{
		"GroupId":   groupID,
		"URL":       url,
		"Name":      name,
		"AssetType": assetType,
	}, &raw); err != nil {
		return "", err
	}
	if id, ok := fieldFromResponse(raw, "Id"); ok {
		return id, nil
	}
	// 解析不出 Id：优先解析业务错误 Code（HTTP 200 但 data 里带 Code 的形态），
	// 让上层能按 Code 分类停止重试；解析不出 Code 再退回带原始响应体的错误。
	if apiErr := errorFromResponse(raw); apiErr != nil {
		return "", apiErr
	}
	return "", fmt.Errorf("seedance CreateAsset: 响应无 Id: %s", string(raw))
}

// GetAsset 查素材当前状态。Status ∈ Active / Failed / (处理中的其他值)，只有 Active 可用。
func (c *Client) GetAsset(ctx context.Context, sdKey, id string) (string, error) {
	var raw json.RawMessage
	if err := c.post(ctx, sdKey, "GetAsset", func(p Paths) string { return p.GetAsset }, map[string]any{"Id": id}, &raw); err != nil {
		return "", err
	}
	if st, ok := fieldFromResponse(raw, "Status"); ok {
		return st, nil
	}
	if apiErr := errorFromResponse(raw); apiErr != nil {
		return "", apiErr
	}
	return "", fmt.Errorf("seedance GetAsset: 响应无 Status: %s", string(raw))
}
