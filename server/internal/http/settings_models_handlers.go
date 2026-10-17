package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

func init() { settingsRouteRegistrars = append(settingsRouteRegistrars, registerModelSettings) }

func registerModelSettings(r chi.Router, d SettingsCenterDeps) {
	if d.Models == nil || d.ModelEdit == nil || d.Settings == nil {
		return
	}
	h := &modelSettingsHandler{d: d}
	r.Get("/models", h.list)
	r.Patch("/models/{id}", h.patch)
	r.Post("/models/{id}/restore-default", h.restore)
	r.Post("/models/test-connection", h.testConnection)
}

type modelSettingsHandler struct{ d SettingsCenterDeps }

// settingsModelView 是设置页看到的模型：可改字段 + 只读技术字段；计费相关字段不下发。
type settingsModelView struct {
	ID               int64             `json:"id"`
	Type             string            `json:"type"`
	Name             string            `json:"name"`
	ModelID          string            `json:"modelId"`
	Enabled          bool              `json:"enabled"`
	Hidden           bool              `json:"hidden"`
	SortOrder        int               `json:"sortOrder"`
	BasePath         string            `json:"basePath"`
	ImageInputFormat string            `json:"imageInputFormat"`
	ImageRequestMode string            `json:"imageRequestMode"`
	RequestTemplate  json.RawMessage   `json:"requestTemplate"`
	PollingTemplate  json.RawMessage   `json:"pollingTemplate"`
	UISchema         json.RawMessage   `json:"uiSchema"`
	GenControl       json.RawMessage   `json:"genControl"`
	ModifiedFields   []string          `json:"modifiedFields"`
	References       []string          `json:"references"`
	Upstream         modelUpstreamView `json:"upstream"`
}

// modelUpstreamView 是每模型独立上游的展示形态：Key 只给掩码（规则同全局），明文永不出 sidecar。
type modelUpstreamView struct {
	Configured   bool   `json:"configured"`
	BaseURL      string `json:"baseUrl"`
	APIKeyMasked string `json:"apiKeyMasked"`
}

func toUpstreamView(m models.ModelConfig) modelUpstreamView {
	if !m.HasOwnUpstream() {
		return modelUpstreamView{}
	}
	return modelUpstreamView{Configured: true, BaseURL: m.UpstreamBaseURL, APIKeyMasked: maskAPIKey(m.UpstreamAPIKey)}
}

func toModelView(m models.ModelConfig, modified []string, refs []string) settingsModelView {
	if modified == nil {
		modified = []string{}
	}
	if refs == nil {
		refs = []string{}
	}
	return settingsModelView{
		ID: m.ID, Type: m.Type, Name: m.Name, ModelID: m.ModelID, Enabled: m.Enabled, Hidden: m.Hidden,
		SortOrder: m.SortOrder, BasePath: m.BasePath, ImageInputFormat: m.ImageInputFormat, ImageRequestMode: m.ImageRequestMode,
		RequestTemplate: nonEmptyRaw(m.RequestTemplate), PollingTemplate: nonEmptyRaw(m.PollingTemplate),
		UISchema: nonEmptyRaw(m.UISchema), GenControl: nonEmptyRaw(m.GenControlRaw),
		ModifiedFields: modified, References: refs,
		Upstream: toUpstreamView(m),
	}
}

// nonEmptyRaw 把空 RawMessage 变成 nil，序列化为 null 而不是非法的空值。
func nonEmptyRaw(r json.RawMessage) json.RawMessage {
	if len(strings.TrimSpace(string(r))) == 0 {
		return nil
	}
	return r
}

// modelReferences 汇总每个 modelId 被哪些配置 / Agent 角色引用。损坏的配置跳过，不阻塞模型页。
func modelReferences(ctx context.Context, d SettingsCenterDeps) (map[string][]string, error) {
	out := map[string][]string{}
	add := func(id, label string) {
		for _, l := range out[id] {
			if l == label {
				return
			}
		}
		out[id] = append(out[id], label)
	}
	for _, key := range settingsConfigKeys {
		raw, err := d.Settings.Get(key)
		if err != nil {
			return nil, err
		}
		refs, err := extractModelRefs(key, []byte(raw))
		if err != nil {
			continue
		}
		for _, r := range refs {
			add(r.ModelID, settingsConfigLabels[key])
		}
	}
	if d.Agent != nil {
		items, err := d.Agent.ListAgentModels(ctx)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			add(it.ModelName, "Agent · "+it.Alias)
		}
	}
	return out, nil
}

func (h *modelSettingsHandler) list(w nethttp.ResponseWriter, r *nethttp.Request) {
	list, err := h.d.Models.List(false)
	if err != nil {
		scWriteError(w, err)
		return
	}
	mods, err := h.d.ModelEdit.ModifiedModelFields(r.Context())
	if err != nil {
		scWriteError(w, err)
		return
	}
	refs, err := modelReferences(r.Context(), h.d)
	if err != nil {
		scWriteError(w, err)
		return
	}
	out := make([]settingsModelView, 0, len(list))
	for _, m := range list {
		out = append(out, toModelView(m, mods[m.ID], refs[m.ModelID]))
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"data": out})
}

func (h *modelSettingsHandler) one(r *nethttp.Request, id int64) (settingsModelView, error) {
	m, err := h.d.Models.GetByID(id)
	if err != nil {
		return settingsModelView{}, err
	}
	mods, err := h.d.ModelEdit.ModifiedModelFields(r.Context())
	if err != nil {
		return settingsModelView{}, err
	}
	refs, err := modelReferences(r.Context(), h.d)
	if err != nil {
		return settingsModelView{}, err
	}
	return toModelView(*m, mods[id], refs[m.ModelID]), nil
}

func (h *modelSettingsHandler) patch(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := parseSettingsID(r)
	if err != nil {
		scWriteError(w, err)
		return
	}
	var p ModelPatch
	if err := scDecodeStrict(w, r, &p); err != nil {
		scWriteError(w, err)
		return
	}
	if p.Empty() {
		scWriteError(w, scValidation("", "没有要修改的字段"))
		return
	}
	m, err := h.d.Models.GetByID(id)
	if err != nil {
		scWriteError(w, err)
		return
	}
	if err := validateModelPatch(m, &p); err != nil {
		scWriteError(w, err)
		return
	}
	upstream, err := resolveUpstreamPatch(m, p.Upstream)
	if err != nil {
		scWriteError(w, err)
		return
	}
	if upstream != nil && h.d.ModelUpstream == nil {
		scWriteError(w, scValidation("upstream", "当前版本不支持单独配置上游"))
		return
	}
	if p.Enabled != nil && !*p.Enabled && m.Enabled {
		refs, err := modelReferences(r.Context(), h.d)
		if err != nil {
			scWriteError(w, err)
			return
		}
		if users := refs[m.ModelID]; len(users) > 0 {
			scWriteError(w, scValidation("enabled", "该模型正被以下设置使用："+strings.Join(users, "、")+"。请先在对应设置里换成其他模型，再停用"))
			return
		}
	}
	if !p.BuiltinEmpty() {
		if err := h.d.ModelEdit.UpdateModel(r.Context(), id, p); err != nil {
			scWriteError(w, err)
			return
		}
	}
	if upstream != nil {
		if err := h.d.ModelUpstream.SetUpstream(id, upstream.baseURL, upstream.apiKey); err != nil {
			scWriteError(w, err)
			return
		}
	}
	v, err := h.one(r, id)
	if err != nil {
		scWriteError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, v)
}

func (h *modelSettingsHandler) restore(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := parseSettingsID(r)
	if err != nil {
		scWriteError(w, err)
		return
	}
	if err := h.d.ModelEdit.RestoreModelDefault(r.Context(), id); err != nil {
		scWriteError(w, err)
		return
	}
	v, err := h.one(r, id)
	if err != nil {
		scWriteError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, v)
}

func intInRange(field, label string, v *int, lo, hi int) error {
	if v != nil && (*v < lo || *v > hi) {
		return scValidation(field, fmt.Sprintf("%s必须在 %d 到 %d 之间", label, lo, hi))
	}
	return nil
}

// validateModelPatch 本地校验取值范围；字段是否允许修改由严格解码和 builtin 编辑策略共同把关。
func validateModelPatch(m *models.ModelConfig, p *ModelPatch) error {
	if p.Name != nil {
		n := strings.TrimSpace(*p.Name)
		if n == "" {
			return scValidation("name", "显示名不能为空")
		}
		if utf8.RuneCountInString(n) > 64 {
			return scValidation("name", "显示名最多 64 个字")
		}
		p.Name = &n
	}
	for _, c := range []struct {
		field, label string
		v            *int
		lo, hi       int
	}{
		{"sortOrder", "排序", p.SortOrder, -1000000, 1000000},
		{"concurrency", "并发上限", p.Concurrency, 0, 200},
		{"canvasMaxRetries", "最大重试次数", p.CanvasMaxRetries, 0, 10},
		{"canvasRetryIntervalSec", "重试间隔", p.CanvasRetryIntervalSec, 0, 600},
	} {
		if err := intInRange(c.field, c.label, c.v, c.lo, c.hi); err != nil {
			return err
		}
	}
	if len(p.UISchemaDefaults) > 0 {
		return validateUISchemaDefaults(m.UISchema, p.UISchemaDefaults)
	}
	return nil
}

// validateUISchemaDefaults 要求每个默认值都等于该字段 options 中某一项的 value（按 JSON 值比较，"1" ≠ 1）。
func validateUISchemaDefaults(uiSchema json.RawMessage, defaults map[string]json.RawMessage) error {
	var schema map[string]struct {
		Options []struct {
			Value json.RawMessage `json:"value"`
		} `json:"options"`
	}
	if len(uiSchema) > 0 {
		if err := json.Unmarshal(uiSchema, &schema); err != nil {
			return scValidation("uiSchemaDefaults", "该模型的参数定义无法解析，不能修改默认值")
		}
	}
	for field, raw := range defaults {
		def, ok := schema[field]
		if !ok || len(def.Options) == 0 {
			return scValidation("uiSchemaDefaults."+field, "参数「"+field+"」不存在或没有可选项")
		}
		var want any
		if err := json.Unmarshal(raw, &want); err != nil {
			return scValidation("uiSchemaDefaults."+field, "默认值格式不正确")
		}
		matched := false
		for _, o := range def.Options {
			var got any
			if json.Unmarshal(o.Value, &got) == nil && reflect.DeepEqual(got, want) {
				matched = true
				break
			}
		}
		if !matched {
			return scValidation("uiSchemaDefaults."+field, "参数「"+field+"」的默认值必须是可选项之一")
		}
	}
	return nil
}

// errUpstreamUnpaired 是“单独配置上游时两项必填”的用户可见错误（文案见契约）。
const errUpstreamUnpairedMsg = "单独配置上游时，地址和 API Key 都必须填写"

// upstreamWrite 是解析后要落库的独立上游；两项都为空表示清空。
type upstreamWrite struct{ baseURL, apiKey string }

// resolveUpstreamPatch 解析 PATCH 里的 upstream 字段：
//   - 缺省：返回 nil，不改动；
//   - null：清空，恢复走全局；
//   - {baseUrl, apiKey?}：baseUrl 按全局地址规则校验；apiKey 缺省或空串时沿用原 Key；
//     最终两项都必须非空，否则 400。
//
// 严格解码（未知字段 400）。错误信息里绝不带 Key 内容。
func resolveUpstreamPatch(m *models.ModelConfig, raw json.RawMessage) (*upstreamWrite, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return &upstreamWrite{}, nil
	}
	var body struct {
		BaseURL *string `json:"baseUrl"`
		APIKey  *string `json:"apiKey"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		const unknown = "json: unknown field "
		if msg := err.Error(); strings.HasPrefix(msg, unknown) {
			f := strings.Trim(strings.TrimPrefix(msg, unknown), `"`)
			return nil, scValidation("upstream."+f, "字段「upstream."+f+"」不允许修改")
		}
		return nil, scValidation("upstream", "upstream 格式不正确")
	}
	base := ""
	if body.BaseURL != nil {
		base = strings.TrimSpace(*body.BaseURL)
	}
	key := ""
	if body.APIKey != nil {
		key = strings.TrimSpace(*body.APIKey)
	}
	if key == "" {
		key = m.UpstreamAPIKey // 只改地址：保留原 Key
	}
	if base == "" || key == "" {
		return nil, scValidation("upstream", errUpstreamUnpairedMsg)
	}
	nb, err := normalizeBaseDomain(base)
	if err != nil {
		return nil, remapField(err, "upstream.baseUrl")
	}
	nk, err := normalizeAPIKey(key)
	if err != nil {
		return nil, remapField(err, "upstream.apiKey")
	}
	return &upstreamWrite{baseURL: nb, apiKey: nk}, nil
}

// remapField 把复用的全局校验错误改挂到独立上游的字段名上。
func remapField(err error, field string) error {
	var ve *SettingsValidationError
	if errors.As(err, &ve) {
		return scValidation(field, ve.Message)
	}
	return err
}
