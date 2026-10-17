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

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/upstream"
)

// ModelID 是合规校验专用的隐藏内置模型（seedance-asset 设计 §2）。
const ModelID = "seedance-asset"

// AssetType 取值（上游 Seedance 素材类型）。
const (
	AssetTypeImage = "Image"
	AssetTypeVideo = "Video"
	AssetTypeAudio = "Audio"
)

const registerTimeout = 300 * time.Second

// ErrModelUnavailable：模型不存在、被禁用，或解析不出上游地址与 Key。
var ErrModelUnavailable = errors.New("合规校验模型 seedance-asset 未启用或上游未配置")

type ModelLookup interface {
	GetByModelID(modelID string) (*models.ModelConfig, error)
}

// UpstreamError 携带上游错误原文，界面原样显示。
type UpstreamError struct {
	Status  int
	Message string
}

func (e *UpstreamError) Error() string { return e.Message }

// Registrar 同步调用 seedance-asset，把公网 URL 注册为 Seedance 素材，返回 asset_id。
type Registrar struct {
	models   ModelLookup
	settings models.UpstreamSettings
	http     *http.Client
}

func NewRegistrar(m ModelLookup, settings models.UpstreamSettings, httpClient *http.Client) *Registrar {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: registerTimeout}
	}
	return &Registrar{models: m, settings: settings, http: httpClient}
}

// resolve 取模型与上游；任何一项缺失都返回 ErrModelUnavailable。
func (r *Registrar) resolve() (*models.ModelConfig, string, string, error) {
	m, err := r.models.GetByModelID(ModelID)
	if err != nil || m == nil || !m.Enabled {
		return nil, "", "", ErrModelUnavailable
	}
	base, key, err := models.ResolveUpstream(m, r.settings)
	if err != nil || strings.TrimSpace(base) == "" || strings.TrimSpace(key) == "" {
		return nil, "", "", ErrModelUnavailable
	}
	return m, strings.TrimRight(base, "/"), key, nil
}

func (r *Registrar) Configured() bool {
	_, _, _, err := r.resolve()
	return err == nil
}

func (r *Registrar) Register(ctx context.Context, url, assetType string) (string, error) {
	m, base, key, err := r.resolve()
	if err != nil {
		return "", err
	}
	body, err := upstream.InterpolateTemplate(m.RequestTemplate, map[string]any{"image": url, "asset_type": assetType})
	if err != nil {
		return "", fmt.Errorf("合规校验请求模板错误: %w", err)
	}
	path := m.BasePath
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := r.http.Do(req)
	if err != nil {
		return "", errors.New(redactKey("合规校验请求失败："+err.Error(), key))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var out struct {
		Metadata struct {
			AssetID string `json:"asset_id"`
		} `json:"metadata"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode/100 == 2 && out.Metadata.AssetID != "" {
		return out.Metadata.AssetID, nil
	}
	msg := ""
	if out.Error != nil {
		msg = strings.TrimSpace(out.Error.Message)
	}
	if msg == "" {
		// 先脱敏再截断：反过来做，Key 横跨截断点时会漏出半截。
		msg = truncateRunes(redactKey(strings.TrimSpace(string(raw)), key), 500)
	}
	if msg == "" {
		msg = fmt.Sprintf("上游未返回 asset_id（HTTP %d）", resp.StatusCode)
	} else if resp.StatusCode/100 == 2 && out.Error == nil {
		msg = "上游未返回 asset_id：" + msg
	}
	return "", &UpstreamError{Status: resp.StatusCode, Message: redactKey(msg, key)}
}

func redactKey(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "***")
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
