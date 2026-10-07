package http

// 把 P2a 的 builtin 编辑服务、P5 的 Agent 仓库接到设置页接口上（P4 Task 15）。
// 只有这个文件知道其他计划的具体类型；名称变化只改这里。

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/agent"
	"github.com/james/huabu_studio/apps/api/internal/builtin"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/settings"
)

func mapPolicyErr(err error) error {
	var ve *builtin.ValidationError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ve):
		return &SettingsValidationError{Field: ve.Path, Message: ve.Msg}
	case errors.Is(err, builtin.ErrNotFound):
		return ErrSettingsNotFound
	}
	return err
}

var pathIdentRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// modelPatchToEdits 把补丁转成 P2a 的字段路径编辑（路径语法见 P2a path.go）。
func modelPatchToEdits(p ModelPatch) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	put := func(path string, v any) {
		b, _ := json.Marshal(v)
		out[path] = b
	}
	if p.Name != nil {
		put("name", *p.Name)
	}
	if p.Enabled != nil {
		put("enabled", *p.Enabled)
	}
	if p.Hidden != nil {
		put("hidden", *p.Hidden)
	}
	if p.SortOrder != nil {
		put("sort_order", *p.SortOrder)
	}
	if p.Concurrency != nil {
		put("gen_control.concurrency", *p.Concurrency)
	}
	if p.CanvasMaxRetries != nil {
		put("gen_control.modules.canvas.maxRetries", *p.CanvasMaxRetries)
	}
	if p.CanvasRetryIntervalSec != nil {
		put("gen_control.modules.canvas.retryIntervalSec", *p.CanvasRetryIntervalSec)
	}
	for k, v := range p.UISchemaDefaults {
		if !pathIdentRe.MatchString(k) {
			return nil, scValidation("uiSchemaDefaults."+k, "参数名不合法")
		}
		out["ui_schema."+k+".default"] = v
	}
	return out, nil
}

func overrideToPatchField(path string) string {
	switch {
	case path == "sort_order":
		return "sortOrder"
	case strings.HasPrefix(path, "ui_schema.") && strings.HasSuffix(path, ".default"):
		return "uiSchemaDefaults"
	case path == "gen_control.concurrency":
		return "concurrency"
	case path == "gen_control.modules.canvas.maxRetries":
		return "canvasMaxRetries"
	case path == "gen_control.modules.canvas.retryIntervalSec":
		return "canvasRetryIntervalSec"
	}
	return path
}

type policyModelEdit struct {
	svc  *builtin.Service
	repo models.Repository
}

func NewPolicyModelEdit(svc *builtin.Service, repo models.Repository) ModelEditService {
	return policyModelEdit{svc: svc, repo: repo}
}

func (a policyModelEdit) UpdateModel(ctx context.Context, id int64, p ModelPatch) error {
	m, err := a.repo.GetByID(id)
	if err != nil {
		return err
	}
	edits, err := modelPatchToEdits(p)
	if err != nil {
		return err
	}
	_, err = a.svc.UpdateModel(ctx, m.ModelID, edits)
	return mapPolicyErr(err)
}

func (a policyModelEdit) RestoreModelDefault(ctx context.Context, id int64) error {
	m, err := a.repo.GetByID(id)
	if err != nil {
		return err
	}
	_, err = a.svc.RestoreModelDefault(ctx, m.ModelID)
	return mapPolicyErr(err)
}

func (a policyModelEdit) ModifiedModelFields(ctx context.Context) (map[int64][]string, error) {
	states, err := a.svc.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64][]string{}
	for _, st := range states {
		seen := map[string]bool{}
		for _, o := range st.Overrides {
			f := overrideToPatchField(o)
			if !seen[f] {
				seen[f] = true
				out[st.ID] = append(out[st.ID], f)
			}
		}
	}
	return out, nil
}

type policySettingEdit struct {
	svc  *builtin.Service
	repo settings.Repository
}

func NewPolicySettingEdit(svc *builtin.Service, repo settings.Repository) SettingEditService {
	return policySettingEdit{svc: svc, repo: repo}
}

func (a policySettingEdit) UpdateSetting(ctx context.Context, key, value string) error {
	if key == settingBaseDomain {
		b, _ := json.Marshal(value)
		_, err := a.svc.UpdateSetting(ctx, key, map[string]json.RawMessage{"$": b})
		return mapPolicyErr(err)
	}
	patterns, ok := builtin.SettingEditableFields(key)
	if !ok {
		return ErrSettingsNotFound
	}
	curRaw, err := a.repo.Get(key)
	if err != nil {
		return err
	}
	var cur, next any
	if curRaw != "" {
		if err := json.Unmarshal([]byte(curRaw), &cur); err != nil {
			return err
		}
	}
	if err := json.Unmarshal([]byte(value), &next); err != nil {
		return scValidation("value", "配置不是合法的 JSON")
	}
	// 白名单强制执行（审查 I7）：可改路径以外有任何变化（增删条目、改 key / rotation / direction /
	// styles、加新字段……）整个请求 400，一个字段都不写，而不是静默丢掉。
	if nonEditableChanged(patterns, cur, next) {
		return scValidation("value", "只能修改开放的字段（适用模型、模板、提示词等），不能新增或删除条目、修改其他字段")
	}
	edits, err := settingEdits(patterns, cur, next)
	if err != nil {
		return err
	}
	if len(edits) == 0 {
		return nil // 可改字段没有变化
	}
	_, err = a.svc.UpdateSetting(ctx, key, edits)
	return mapPolicyErr(err)
}

func (a policySettingEdit) RestoreSettingDefault(ctx context.Context, key string) error {
	_, err := a.svc.RestoreSettingDefault(ctx, key)
	return mapPolicyErr(err)
}

func (a policySettingEdit) ModifiedSettings(ctx context.Context) ([]string, error) {
	states, err := a.svc.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, st := range states {
		if len(st.Overrides) > 0 {
			out = append(out, st.Key)
		}
	}
	return out, nil
}

// settingEdits 在 next 上展开每个可改模式的具体路径，返回与 cur 相比值有变化的那些。
// 数组段按元素的 "key" 属性寻址（P2a 路径语法），新旧文档都按 key 取值，顺序变化不算修改。
func settingEdits(patterns []string, cur, next any) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for _, pat := range patterns {
		for _, path := range expandPaths(next, strings.Split(pat, "."), "") {
			nv, _ := valueAt(next, strings.Split(path, "."))
			ov, had := valueAt(cur, strings.Split(path, "."))
			if had && reflect.DeepEqual(nv, ov) {
				continue
			}
			b, err := json.Marshal(nv)
			if err != nil {
				return nil, err
			}
			out[path] = b
		}
	}
	return out, nil
}

// nonEditableChanged 报告 next 相对 cur 是否改了白名单以外的内容：把 next 上每个可改路径的值换回
// cur 的值（cur 里没有这个路径就删掉），剩下的必须与 cur 完全相同。数组按元素 key 寻址，
// 所以新增 / 删除 / 改 key 的条目都会留下差异；key 不合法的元素不参与替换，同样会留下差异。
func nonEditableChanged(patterns []string, cur, next any) bool {
	masked := deepCopyJSON(next)
	var paths []string
	for _, pat := range patterns {
		paths = append(paths, expandPaths(masked, strings.Split(pat, "."), "")...)
	}
	for _, path := range paths {
		segs := strings.Split(path, ".")
		if ov, ok := valueAt(cur, segs); ok {
			setAt(masked, segs, deepCopyJSON(ov))
		} else {
			deleteAt(masked, segs)
		}
	}
	return !reflect.DeepEqual(masked, cur)
}

func deepCopyJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = deepCopyJSON(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = deepCopyJSON(x)
		}
		return out
	}
	return v
}

// parentAt 返回路径最后一段所在的对象和字段名；最后一段带 [key] 时不支持（白名单模式都以普通字段结尾）。
func parentAt(doc any, segs []string) (map[string]any, string, bool) {
	last := segs[len(segs)-1]
	if strings.Contains(last, "[") {
		return nil, "", false
	}
	parent := doc
	if len(segs) > 1 {
		var ok bool
		if parent, ok = valueAt(doc, segs[:len(segs)-1]); !ok {
			return nil, "", false
		}
	}
	obj, ok := parent.(map[string]any)
	return obj, last, ok
}

func setAt(doc any, segs []string, v any) {
	if obj, name, ok := parentAt(doc, segs); ok {
		obj[name] = v
	}
}

func deleteAt(doc any, segs []string) {
	if obj, name, ok := parentAt(doc, segs); ok {
		delete(obj, name)
	}
}

// expandPaths 把模式（如 presets[*].prompt）在 doc 上展开成具体路径（presets[fisheye].prompt）。
func expandPaths(doc any, segs []string, prefix string) []string {
	if len(segs) == 0 {
		return []string{prefix}
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return nil
	}
	seg := segs[0]
	join := func(s string) string {
		if prefix == "" {
			return s
		}
		return prefix + "." + s
	}
	name, keyed := strings.CutSuffix(seg, "[*]")
	if !keyed {
		if _, exists := obj[seg]; !exists {
			return nil
		}
		return expandPaths(obj[seg], segs[1:], join(seg))
	}
	arr, _ := obj[name].([]any)
	var out []string
	for _, el := range arr {
		m, _ := el.(map[string]any)
		k, _ := m["key"].(string)
		if !pathIdentRe.MatchString(k) {
			continue
		}
		out = append(out, expandPaths(m, segs[1:], join(name+"["+k+"]"))...)
	}
	return out
}

// valueAt 按具体路径取值；name[key] 段在数组里找 key 属性相等的元素。
func valueAt(doc any, segs []string) (any, bool) {
	cur := doc
	for _, seg := range segs {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if i := strings.IndexByte(seg, '['); i >= 0 && strings.HasSuffix(seg, "]") {
			name, key := seg[:i], seg[i+1:len(seg)-1]
			arr, _ := obj[name].([]any)
			found := false
			for _, el := range arr {
				if m, ok := el.(map[string]any); ok && m["key"] == key {
					cur, found = m, true
					break
				}
			}
			if !found {
				return nil, false
			}
			continue
		}
		v, ok := obj[seg]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// backboneAgentModels 列出 Agent 大模型，只供模型中心的“被引用”标注使用。
// Agent 模型写死为内置值（2026-10-07 用户要求）：没有切换和恢复默认。
type backboneAgentModels struct {
	list func() ([]agent.BackboneModel, error)
}

func NewBackboneAgentModels(repo agent.BackboneRepo) AgentModelService {
	return &backboneAgentModels{list: repo.List}
}

func (a *backboneAgentModels) ListAgentModels(context.Context) ([]AgentModelItem, error) {
	rows, err := a.list()
	if err != nil {
		return nil, err
	}
	out := make([]AgentModelItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, AgentModelItem{ID: r.ID, Role: r.Role, Alias: r.Alias, ModelName: r.ModelName})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
