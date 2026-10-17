package http

// 设置页接口的公共部分：与 builtin 编辑策略服务、Agent 仓库之间的窄接口，
// 错误约定，以及路由注册表。各接口的 handler 在各自文件里通过 init() 注册，
// 互不改动对方文件；依赖为 nil 的那部分接口不注册。

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	nethttp "net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/settings"
)

// ModelPatch 是模型中心允许用户修改的全部字段。指针为 nil 表示不改。
// 请求体严格解码：出现这里没有的字段（如 requestTemplate）直接 400。
type ModelPatch struct {
	Enabled                *bool                      `json:"enabled,omitempty"`
	Hidden                 *bool                      `json:"hidden,omitempty"`
	Name                   *string                    `json:"name,omitempty"`
	SortOrder              *int                       `json:"sortOrder,omitempty"`
	UISchemaDefaults       map[string]json.RawMessage `json:"uiSchemaDefaults,omitempty"`
	Concurrency            *int                       `json:"concurrency,omitempty"`
	CanvasMaxRetries       *int                       `json:"canvasMaxRetries,omitempty"`
	CanvasRetryIntervalSec *int                       `json:"canvasRetryIntervalSec,omitempty"`
	// Upstream 是每模型独立上游（本机字段，不属于内置字段白名单 / user_overrides）：
	// 缺省 = 不改；null = 清空回落全局；{baseUrl, apiKey?} = 设置。原文保留，由 handler 严格解析。
	Upstream json.RawMessage `json:"upstream,omitempty"`
}

// Empty 报告补丁里是否一个字段都没有。
func (p ModelPatch) Empty() bool {
	return p.BuiltinEmpty() && len(p.Upstream) == 0
}

// BuiltinEmpty 报告补丁里是否没有任何经内置编辑服务修改的字段（不看 Upstream）。
func (p ModelPatch) BuiltinEmpty() bool {
	return p.Enabled == nil && p.Hidden == nil && p.Name == nil && p.SortOrder == nil &&
		len(p.UISchemaDefaults) == 0 && p.Concurrency == nil && p.CanvasMaxRetries == nil && p.CanvasRetryIntervalSec == nil
}

// ModelUpstreamStore 写模型的独立上游（models.SQLiteRepository 直接满足）。两项都为空 = 清空。
type ModelUpstreamStore interface {
	SetUpstream(id int64, baseURL, apiKey string) error
}

// ModelEditService 由 builtin 包的编辑策略服务实现（经 settings_center_adapter.go 适配）。
type ModelEditService interface {
	UpdateModel(ctx context.Context, id int64, p ModelPatch) error
	RestoreModelDefault(ctx context.Context, id int64) error
	// ModifiedModelFields 返回每个模型被用户改过的字段名（ModelPatch 的 json 名）。
	ModifiedModelFields(ctx context.Context) (map[int64][]string, error)
}

// SettingEditService 由 builtin 包的编辑策略服务实现：只允许可改路径变化。
type SettingEditService interface {
	UpdateSetting(ctx context.Context, key, value string) error
	RestoreSettingDefault(ctx context.Context, key string) error
	ModifiedSettings(ctx context.Context) ([]string, error)
}

// AgentModelItem 是一个 Agent 大模型（只读）。
type AgentModelItem struct {
	ID        int64  `json:"id"`
	Role      string `json:"role"`
	Alias     string `json:"alias"`
	ModelName string `json:"modelName"`
}

// AgentModelService 列出 Agent 大模型，供模型中心标注“被 Agent 引用”。
// Agent 模型写死为内置值，不提供切换 / 恢复默认，设置页也没有 Agent 模型页。
type AgentModelService interface {
	ListAgentModels(ctx context.Context) ([]AgentModelItem, error)
}

// SettingsValidationError 是面向用户的校验错误，映射为 400。
type SettingsValidationError struct {
	Field   string
	Message string
}

func (e *SettingsValidationError) Error() string { return e.Message }

// ErrSettingsNotFound 表示要修改或恢复的对象不存在，映射为 404。
var ErrSettingsNotFound = errors.New("settings: not found")

// SettingsCenterDeps 是设置接口的依赖。DataDir、Version 为空时由 NewRouter 用 Deps 的同名字段补齐。
type SettingsCenterDeps struct {
	Settings settings.Repository
	Models   models.Repository
	// ModelUpstream 为 nil 时 PATCH 里的 upstream 字段一律 400。
	ModelUpstream ModelUpstreamStore
	ModelEdit     ModelEditService
	SettingEdit   SettingEditService
	Agent         AgentModelService
	// ObjectStore 为 nil 时不注册 /object-storage/test（其余对象存储接口只依赖 Settings）。
	ObjectStore ObjectStoreProber
	DataDir     string
	Version     string
}

// settingsRouteRegistrars 由各 handler 文件在 init() 里追加；路径相对 /api/v1/settings。
var settingsRouteRegistrars []func(r chi.Router, d SettingsCenterDeps)

func mountSettingsCenter(r chi.Router, d SettingsCenterDeps) {
	r.Route("/api/v1/settings", func(sr chi.Router) {
		for _, reg := range settingsRouteRegistrars {
			reg(sr, d)
		}
	})
}

func scValidation(field, msg string) error {
	return &SettingsValidationError{Field: field, Message: msg}
}

// parseSettingsID 解析路径参数 {id}（模型、Agent 模型共用）。
func parseSettingsID(r *nethttp.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, scValidation("id", "无效的 id")
	}
	return id, nil
}

func scWriteError(w nethttp.ResponseWriter, err error) {
	var ve *SettingsValidationError
	switch {
	case errors.As(err, &ve):
		body := map[string]any{"error": ve.Message}
		if ve.Field != "" {
			body["field"] = ve.Field
		}
		writeJSON(w, nethttp.StatusBadRequest, body)
	case errors.Is(err, models.ErrUpstreamUnpaired):
		// handler 已先校验，正常走不到；别的调用方漏校验时也应是 400 而非 500。
		writeJSON(w, nethttp.StatusBadRequest, map[string]any{"error": models.ErrUpstreamUnpaired.Error(), "field": "upstream"})
	case errors.Is(err, ErrSettingsNotFound), errors.Is(err, models.ErrNotFound):
		writeJSON(w, nethttp.StatusNotFound, map[string]any{"error": "要修改的项目不存在"})
	default:
		log.Printf("设置接口内部错误: %v", err)
		writeJSON(w, nethttp.StatusInternalServerError, map[string]any{"error": "保存失败，请查看日志"})
	}
}

const scMaxBody = 2 << 20

// scDecodeStrict 解码 JSON 请求体：未知字段、多余内容、超过 2MB 一律返回 *SettingsValidationError。
func scDecodeStrict(w nethttp.ResponseWriter, r *nethttp.Request, v any) error {
	dec := json.NewDecoder(nethttp.MaxBytesReader(w, r.Body, scMaxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		const unknown = "json: unknown field "
		if msg := err.Error(); strings.HasPrefix(msg, unknown) {
			f := strings.Trim(strings.TrimPrefix(msg, unknown), `"`)
			return scValidation(f, "字段「"+f+"」不允许修改")
		}
		var mbe *nethttp.MaxBytesError
		if errors.As(err, &mbe) {
			return scValidation("", "请求内容过大")
		}
		return scValidation("", "请求格式不正确")
	}
	if dec.More() {
		return scValidation("", "请求格式不正确")
	}
	return nil
}
