package builtin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// 可改字段白名单（spec M10）。模型和每个内置配置各有一份 policy：只有命中某条规则模式的路径
// 才允许修改，且新值必须通过该规则的校验。白名单之外的字段（请求/轮询模板、base_path、图片协议、
// 计费、预设的结构字段……）对用户只读，升级时永远跟随新版种子。

// 内置配置的 key。
const (
	SettingBaseDomain         = "base_domain"
	SettingUpstreamAPIKey     = "upstream_api_key" // 不在种子里，用户自己填（spec M3）；本包的合并永远不碰它
	SettingCanvasLightControl = "canvas_light_control"
	SettingCanvasMultiAngle   = "canvas_multi_angle"
	SettingCanvasPostprocess  = "canvas_postprocess_config"
	SettingCanvasScript       = "canvas_script_config"
	SettingGenImagePresets    = "gen_image_prompt_presets"
)

type valueKind int

const (
	kindJSON valueKind = iota // settings.value 是 JSON 文本
	kindText                  // settings.value 是纯文本（base_domain）
)

// ValidationError 是一次编辑里第一个不合法的字段。P4 的 HTTP 层把它映射成 400。
type ValidationError struct {
	Path string
	Msg  string
}

func (e *ValidationError) Error() string { return e.Path + "：" + e.Msg }

// checkFunc 校验把路径 p 设为 v 是否合法。doc 是写入前的整份文档，用来查同级结构（如 uiSchema 的 options）。
type checkFunc func(doc any, p Path, v any, cat Catalog) error

// pruneFunc 在合并（overlay）时先把用户保存的值对齐到新版结构，再交给 check：
// 例如剔除已下线的模型。返回 ok=false 表示整条覆盖作废。
type pruneFunc func(base any, p Path, user any, cat Catalog) (any, bool)

type rule struct {
	pattern Path
	check   checkFunc
	prune   pruneFunc
}

type policy struct {
	kind  valueKind
	rules []rule
}

type ruleDef struct {
	pattern string
	check   checkFunc
	prune   pruneFunc
}

func newPolicy(kind valueKind, defs ...ruleDef) policy {
	pl := policy{kind: kind}
	for _, d := range defs {
		pl.rules = append(pl.rules, rule{pattern: mustParsePath(d.pattern), check: d.check, prune: d.prune})
	}
	return pl
}

func (pl policy) find(p Path) (rule, bool) {
	for _, r := range pl.rules {
		if r.pattern.Match(p) {
			return r, true
		}
	}
	return rule{}, false
}

// Patterns 返回白名单模式字符串，供 P4 的设置页决定哪些控件可编辑。
func (pl policy) Patterns() []string {
	out := make([]string, len(pl.rules))
	for i, r := range pl.rules {
		out[i] = r.pattern.String()
	}
	return out
}

var modelPolicy = newPolicy(kindJSON,
	ruleDef{"name", checkName, nil},
	ruleDef{"enabled", checkBool, nil},
	ruleDef{"hidden", checkBool, nil},
	ruleDef{"sort_order", checkInt(-1_000_000, 1_000_000), nil},
	ruleDef{"ui_schema.*.default", checkUIDefault, nil},
	// 0 = 用默认并发 200（models.DefaultGenConcurrency）。drama 模块桌面端不用，不开放。
	ruleDef{"gen_control.concurrency", checkInt(0, 200), nil},
	ruleDef{"gen_control.modules.canvas.maxRetries", checkInt(0, 10), nil},
	ruleDef{"gen_control.modules.canvas.retryIntervalSec", checkInt(0, 600), nil},
)

var settingPolicies = map[string]policy{
	SettingBaseDomain: newPolicy(kindText, ruleDef{"$", checkBaseDomain, nil}),
	SettingCanvasLightControl: newPolicy(kindJSON,
		ruleDef{"models", checkModelList("image"), pruneModelList("image")},
		ruleDef{"template", checkTemplate("{light}"), nil},
		ruleDef{"presets[*].prompt", checkPrompt(true, 4000), nil},
	),
	SettingCanvasMultiAngle: newPolicy(kindJSON,
		ruleDef{"models", checkModelList("image"), pruneModelList("image")},
		ruleDef{"template", checkTemplate("{angle}"), nil},
		ruleDef{"presets[*].prompt", checkPrompt(true, 4000), nil},
	),
	SettingGenImagePresets: newPolicy(kindJSON,
		ruleDef{"models", checkModelList("image"), pruneModelList("image")},
		// 参考仓后台要求预设正文非空（gen_image_preset_handlers.go）；档位片段允许为空（“无效果”档位）。
		ruleDef{"presets[*].value.prompt", checkPrompt(false, 8000), nil},
		ruleDef{"presets[*].value.params[*].options[*].prompt", checkPrompt(true, 2000), nil},
	),
	SettingCanvasPostprocess: newPolicy(kindJSON,
		ruleDef{"subtitleRemovalModels", checkDesubList, pruneToBaseEntries},
		ruleDef{"upscaleModel", checkModelRef("video", true), nil},
	),
	SettingCanvasScript: newPolicy(kindJSON,
		ruleDef{"modelCandidates", checkScriptCandidates, pruneToBaseEntries},
		ruleDef{"concurrency", checkInt(1, 20), nil},
		ruleDef{"maxRetries", checkInt(0, 10), nil},
	),
}

// ModelEditableFields 返回模型的可改字段模式。
func ModelEditableFields() []string { return modelPolicy.Patterns() }

// SettingEditableFields 返回内置配置 key 的可改字段模式；不是内置配置返回 ok=false。
func SettingEditableFields(key string) ([]string, bool) {
	pl, ok := settingPolicies[key]
	if !ok {
		return nil, false
	}
	return pl.Patterns(), true
}

// ---- 校验函数 ----

func checkBool(_ any, _ Path, v any, _ Catalog) error {
	if _, ok := v.(bool); !ok {
		return errors.New("必须是 true 或 false")
	}
	return nil
}

func checkName(_ any, _ Path, v any, _ Catalog) error {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return errors.New("不能为空")
	}
	if s != strings.TrimSpace(s) {
		return errors.New("首尾不能有空白")
	}
	if utf8.RuneCountInString(s) > 100 {
		return errors.New("不能超过 100 个字")
	}
	return nil
}

func asInt(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseInt(n.String(), 10, 64)
	return i, err == nil
}

func checkInt(min, max int64) checkFunc {
	return func(_ any, _ Path, v any, _ Catalog) error {
		i, ok := asInt(v)
		if !ok {
			return errors.New("必须是整数")
		}
		if i < min || i > max {
			return fmt.Errorf("必须在 %d 到 %d 之间", min, max)
		}
		return nil
	}
}

// checkUIDefault：uiSchema 参数的默认值必须是该参数 options 里的某个 value。
func checkUIDefault(doc any, p Path, v any, _ Catalog) error {
	opts, ok := getPath(doc, Path{{Name: "ui_schema"}, {Name: p[1].Name}, {Name: "options"}})
	arr, isArr := opts.([]any)
	if !ok || !isArr {
		return fmt.Errorf("参数 %s 没有可选项", p[1].Name)
	}
	for _, o := range arr {
		if m, ok := o.(map[string]any); ok && jsonEqual(m["value"], v) {
			return nil
		}
	}
	return errors.New("不在可选项中")
}

func checkBaseDomain(_ any, _ Path, v any, _ Catalog) error {
	s, ok := v.(string)
	if !ok || s == "" {
		return errors.New("不能为空")
	}
	if s != strings.TrimSpace(s) {
		return errors.New("首尾不能有空白")
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("必须是 http:// 或 https:// 开头的地址")
	}
	// 上游地址 = base_domain + 模型的 base_path（以 / 开头），这里多一个 / 就会拼出 //v1/…。
	// ForceQuery / 原串里的 ? # 单独查：结尾的 "?" "#" 解析后 RawQuery、Fragment 都是空串。
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.User != nil || strings.ContainsAny(s, "?#") {
		return errors.New("只填协议、主机和端口，不要带路径、参数或结尾的 /")
	}
	return nil
}

func checkModelRef(typ string, allowEmpty bool) checkFunc {
	return func(_ any, _ Path, v any, cat Catalog) error {
		s, ok := v.(string)
		if !ok {
			return errors.New("必须是模型 ID 字符串")
		}
		if s == "" && allowEmpty {
			return nil
		}
		return requireModel(cat, s, typ)
	}
}

func requireModel(cat Catalog, id, typ string) error {
	if got, ok := cat[id]; !ok || got != typ {
		return fmt.Errorf("模型 %q 不存在或不是 %s 模型", id, typ)
	}
	return nil
}

func checkModelList(typ string) checkFunc {
	return func(_ any, _ Path, v any, cat Catalog) error {
		arr, ok := v.([]any)
		if !ok {
			return errors.New("必须是模型 ID 数组")
		}
		seen := map[string]bool{}
		for _, e := range arr {
			s, ok := e.(string)
			if !ok {
				return errors.New("必须是模型 ID 数组")
			}
			if err := requireModel(cat, s, typ); err != nil {
				return err
			}
			if seen[s] {
				return fmt.Errorf("模型 %q 重复", s)
			}
			seen[s] = true
		}
		return nil
	}
}

// pruneModelList：用户的模型列表里只剔除已下线（或类型不对）的项，其余原样保留；
// 全部被剔除时整条作废（空列表等于关掉功能，不是用户本意）。
func pruneModelList(typ string) pruneFunc {
	return func(_ any, _ Path, user any, cat Catalog) (any, bool) {
		arr, ok := user.([]any)
		if !ok {
			return nil, false
		}
		out := make([]any, 0, len(arr))
		for _, e := range arr {
			if s, ok := e.(string); ok && requireModel(cat, s, typ) == nil {
				out = append(out, e)
			}
		}
		if len(out) == 0 && len(arr) > 0 {
			return nil, false
		}
		return out, true
	}
}

// pruneToBaseEntries：条目集合以新版种子为准——按 modelId 取用户对应条目的内容，
// 新版新增的条目用内置值，新版下线的条目随之消失。
func pruneToBaseEntries(base any, p Path, user any, _ Catalog) (any, bool) {
	bv, _ := getPath(base, p)
	baseArr, ok := bv.([]any)
	if !ok {
		return nil, false
	}
	userArr, ok := user.([]any)
	if !ok {
		return nil, false
	}
	byID := map[any]any{}
	for _, e := range userArr {
		if m, ok := e.(map[string]any); ok {
			byID[m["modelId"]] = e
		}
	}
	out := make([]any, len(baseArr))
	for i, e := range baseArr {
		out[i] = e
		if m, ok := e.(map[string]any); ok {
			if u, found := byID[m["modelId"]]; found {
				out[i] = u
			}
		}
	}
	return out, true
}

func checkTemplate(placeholder string) checkFunc {
	return func(_ any, _ Path, v any, _ Catalog) error {
		s, ok := v.(string)
		if !ok {
			return errors.New("必须是字符串")
		}
		if utf8.RuneCountInString(s) > 2000 {
			return errors.New("不能超过 2000 个字")
		}
		// 空模板 = 运行时用内置缺省模板（参考仓 canvasgen 的 Default*Template）。
		if s != "" && !strings.Contains(s, placeholder) {
			return fmt.Errorf("必须包含占位符 %s", placeholder)
		}
		return nil
	}
}

func checkPrompt(allowEmpty bool, maxRunes int) checkFunc {
	return func(_ any, _ Path, v any, _ Catalog) error {
		s, ok := v.(string)
		if !ok {
			return errors.New("必须是字符串")
		}
		if !allowEmpty && strings.TrimSpace(s) == "" {
			return errors.New("不能为空")
		}
		if utf8.RuneCountInString(s) > maxRunes {
			return fmt.Errorf("不能超过 %d 个字", maxRunes)
		}
		return nil
	}
}

// objectList 校验 v 是对象数组、每个对象只含 allowed 里的字段。
func objectList(v any, allowed ...string) ([]map[string]any, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, errors.New("必须是数组")
	}
	out := make([]map[string]any, 0, len(arr))
	for i, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("第 %d 项必须是对象", i+1)
		}
		for k := range m {
			if !slices.Contains(allowed, k) {
				return nil, fmt.Errorf("第 %d 项含有不认识的字段 %s", i+1, k)
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// sameEntries 要求 v 与写入前的列表条目一一对应（个数、顺序、idKey 都相同）：
// 这类列表只允许改已有条目的展示与开关字段，不能增删、换模型或调换顺序。
func sameEntries(doc any, p Path, items []map[string]any, idKey string) error {
	curV, _ := getPath(doc, p)
	cur, _ := curV.([]any)
	if len(items) != len(cur) {
		return fmt.Errorf("不能增删条目（应为 %d 项）", len(cur))
	}
	for i, e := range cur {
		m, _ := e.(map[string]any)
		if items[i][idKey] != m[idKey] {
			return fmt.Errorf("第 %d 项的 %s 不能修改或调换顺序", i+1, idKey)
		}
	}
	return nil
}

func checkDesubList(doc any, p Path, v any, cat Catalog) error {
	items, err := objectList(v, "modelId", "label", "subtitle", "isTimeLimited")
	if err != nil {
		return err
	}
	if err := sameEntries(doc, p, items, "modelId"); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, m := range items {
		id, _ := m["modelId"].(string)
		if err := requireModel(cat, id, "video"); err != nil {
			return fmt.Errorf("第 %d 项：%w", i+1, err)
		}
		if seen[id] {
			return fmt.Errorf("模型 %q 重复", id)
		}
		seen[id] = true
		if label, _ := m["label"].(string); strings.TrimSpace(label) == "" {
			return fmt.Errorf("第 %d 项：label 不能为空", i+1)
		}
		if sub, ok := m["subtitle"]; ok {
			if _, isStr := sub.(string); !isStr {
				return fmt.Errorf("第 %d 项：subtitle 必须是字符串", i+1)
			}
		}
		if tl, ok := m["isTimeLimited"]; ok {
			if _, isBool := tl.(bool); !isBool {
				return fmt.Errorf("第 %d 项：isTimeLimited 必须是 true 或 false", i+1)
			}
		}
	}
	return nil
}

func checkScriptCandidates(doc any, p Path, v any, cat Catalog) error {
	items, err := objectList(v, "modelId", "displayName")
	if err != nil {
		return err
	}
	if err := sameEntries(doc, p, items, "modelId"); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, m := range items {
		id, _ := m["modelId"].(string)
		if err := requireModel(cat, id, "text"); err != nil {
			return fmt.Errorf("第 %d 项：%w", i+1, err)
		}
		if seen[id] {
			return fmt.Errorf("模型 %q 重复", id)
		}
		seen[id] = true
		if name, _ := m["displayName"].(string); strings.TrimSpace(name) == "" {
			return fmt.Errorf("第 %d 项：displayName 不能为空", i+1)
		}
	}
	return nil
}

// ---- 编辑与合并（纯函数，不碰数据库）----

// applyEdits 在 cur（当前文档）上应用一批编辑，返回新文档和新的覆盖路径集合（已排序）。
// 全部合法才返回结果，任何一项不合法整体报 *ValidationError，调用方不写库。
// 编辑后的值与 seedDoc（当前二进制的种子）同一路径的值相等时，该路径移出覆盖集合，回到“跟随内置”。
func applyEdits(pl policy, cur, seedDoc any, overrides []string, edits map[string]json.RawMessage, cat Catalog) (any, []string, error) {
	if len(edits) == 0 {
		return nil, nil, &ValidationError{Path: "$", Msg: "没有要修改的字段"}
	}
	doc, err := deepCopy(cur)
	if err != nil {
		return nil, nil, err
	}
	ov := map[string]bool{}
	for _, o := range overrides {
		ov[o] = true
	}
	for _, k := range sortedKeys(edits) {
		p, err := ParsePath(k)
		if err != nil {
			return nil, nil, &ValidationError{Path: k, Msg: err.Error()}
		}
		r, ok := pl.find(p)
		if !ok {
			return nil, nil, &ValidationError{Path: k, Msg: "该字段不可修改"}
		}
		v, err := decodeJSON(edits[k])
		if err != nil {
			return nil, nil, &ValidationError{Path: k, Msg: "不是合法的 JSON：" + err.Error()}
		}
		if err := r.check(doc, p, v, cat); err != nil {
			return nil, nil, &ValidationError{Path: k, Msg: err.Error()}
		}
		if doc, err = setPath(doc, p, v); err != nil {
			return nil, nil, &ValidationError{Path: k, Msg: err.Error()}
		}
		if sv, found := getPath(seedDoc, p); found && jsonEqual(sv, v) {
			delete(ov, p.String())
		} else {
			ov[p.String()] = true
		}
	}
	return doc, sortedKeys(ov), nil
}

// overlay 以 base（新版种子文档）为底，把 user 文档里 overrides 列出的字段逐个盖上去（spec M11）。
// 覆盖项在新版里不再可改、找不到挂载位置（预设或 uiSchema 参数被删）、或不再通过校验
// （选项被删、引用的模型被删）时丢弃，该字段改为跟随新版。返回合成文档、保留的和丢弃的覆盖路径。
func overlay(pl policy, base, user any, overrides []string, cat Catalog) (any, []string, []string) {
	doc, err := deepCopy(base)
	if err != nil {
		return base, nil, overrides
	}
	var kept, dropped []string
	for _, k := range overrides {
		switch applyOne(pl, &doc, user, k, cat) {
		case applied:
			kept = append(kept, k)
		case sameAsSeed:
			// 用户值与新版种子相同：回到“跟随内置”，以后种子再改时才会跟着变。
		default:
			dropped = append(dropped, k)
		}
	}
	return doc, kept, dropped
}

type applyResult int

const (
	rejected applyResult = iota
	applied
	sameAsSeed
)

func applyOne(pl policy, doc *any, user any, k string, cat Catalog) applyResult {
	p, err := ParsePath(k)
	if err != nil {
		return rejected
	}
	r, ok := pl.find(p)
	if !ok {
		return rejected
	}
	v, ok := getPath(user, p)
	if !ok {
		return rejected
	}
	if v, err = deepCopy(v); err != nil {
		return rejected
	}
	if r.prune != nil {
		if v, ok = r.prune(*doc, p, v, cat); !ok {
			return rejected
		}
	}
	if r.check(*doc, p, v, cat) != nil {
		return rejected
	}
	if bv, found := getPath(*doc, p); found && jsonEqual(bv, v) {
		return sameAsSeed
	}
	nd, err := setPath(*doc, p, v)
	if err != nil {
		return rejected
	}
	*doc = nd
	return applied
}

// ---- 行 ↔ 文档 ----

// modelDoc 把模型的可改部分转成文档，字段名与列名一致。
func modelDoc(mp modelParts) (any, error) {
	doc := map[string]any{
		"name":        mp.Name,
		"enabled":     mp.Enabled,
		"hidden":      mp.Hidden,
		"sort_order":  json.Number(strconv.Itoa(mp.SortOrder)),
		"ui_schema":   nil,
		"gen_control": nil,
	}
	for col, raw := range map[string]*string{"ui_schema": mp.UISchema, "gen_control": mp.GenControl} {
		if raw == nil {
			continue
		}
		v, err := decodeJSON([]byte(*raw))
		if err != nil {
			return nil, fmt.Errorf("%s 不是合法 JSON: %w", col, err)
		}
		doc[col] = v
	}
	return doc, nil
}

// partsFromDoc 把文档写回模型的可改部分：被覆盖的字段取 doc，其余一律取 seed。
// ui_schema / gen_control 只要有一个子路径被覆盖就整列重新编码（键序跟种子原文），否则保留种子原文（逐字节一致）。
func partsFromDoc(doc any, seed modelParts, overrides []string) (modelParts, error) {
	m, ok := doc.(map[string]any)
	if !ok {
		return modelParts{}, errors.New("模型文档不是对象")
	}
	has := func(field string) bool { return slices.Contains(overrides, field) }
	hasUnder := func(col string) bool {
		for _, o := range overrides {
			if strings.HasPrefix(o, col+".") {
				return true
			}
		}
		return false
	}
	out := seed
	if has("name") {
		s, ok := m["name"].(string)
		if !ok {
			return modelParts{}, errors.New("name 类型错误")
		}
		out.Name = s
	}
	for field, dst := range map[string]*bool{"enabled": &out.Enabled, "hidden": &out.Hidden} {
		if has(field) {
			b, ok := m[field].(bool)
			if !ok {
				return modelParts{}, fmt.Errorf("%s 类型错误", field)
			}
			*dst = b
		}
	}
	if has("sort_order") {
		i, ok := asInt(m["sort_order"])
		if !ok {
			return modelParts{}, errors.New("sort_order 类型错误")
		}
		out.SortOrder = int(i)
	}
	for col, dst := range map[string]**string{"ui_schema": &out.UISchema, "gen_control": &out.GenControl} {
		if !hasUnder(col) {
			continue
		}
		// 按种子原文的键序编码：ui_schema 的参数顺序就是前端 pill 顺序，按 map 默认的
		// 字母序重编码会让用户只改一个 default 就打乱排列。
		var s string
		var err error
		if *dst != nil {
			s, err = encodeJSONLike(m[col], **dst)
		} else {
			s, err = encodeJSON(m[col])
		}
		if err != nil {
			return modelParts{}, err
		}
		*dst = &s
	}
	return out, nil
}

// settingDoc 把配置值转成文档：纯文本配置的文档就是字符串本身。
func settingDoc(kind valueKind, value string) (any, error) {
	if kind == kindText {
		return value, nil
	}
	return decodeJSON([]byte(value))
}

// settingValueFromDoc 把文档写回配置值：没有覆盖项时原样返回种子值（逐字节一致）。
func settingValueFromDoc(kind valueKind, seedValue string, doc any, overrides []string) (string, error) {
	if len(overrides) == 0 {
		return seedValue, nil
	}
	if kind == kindText {
		s, ok := doc.(string)
		if !ok {
			return "", errors.New("纯文本配置的值必须是字符串")
		}
		return s, nil
	}
	return encodeJSONLike(doc, seedValue)
}
