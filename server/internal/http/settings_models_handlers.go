package http

import (
	"context"
	"encoding/json"
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
}

type modelSettingsHandler struct{ d SettingsCenterDeps }

// settingsModelView 是设置页看到的模型：可改字段 + 只读技术字段；计费相关字段不下发（spec X3）。
type settingsModelView struct {
	ID               int64           `json:"id"`
	Type             string          `json:"type"`
	Name             string          `json:"name"`
	ModelID          string          `json:"modelId"`
	Enabled          bool            `json:"enabled"`
	Hidden           bool            `json:"hidden"`
	SortOrder        int             `json:"sortOrder"`
	BasePath         string          `json:"basePath"`
	ImageInputFormat string          `json:"imageInputFormat"`
	ImageRequestMode string          `json:"imageRequestMode"`
	RequestTemplate  json.RawMessage `json:"requestTemplate"`
	PollingTemplate  json.RawMessage `json:"pollingTemplate"`
	UISchema         json.RawMessage `json:"uiSchema"`
	GenControl       json.RawMessage `json:"genControl"`
	ModifiedFields   []string        `json:"modifiedFields"`
	References       []string        `json:"references"`
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
	if err := h.d.ModelEdit.UpdateModel(r.Context(), id, p); err != nil {
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

// validateModelPatch 本地校验取值范围；字段是否允许修改由严格解码和 P2a 编辑策略共同把关。
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
