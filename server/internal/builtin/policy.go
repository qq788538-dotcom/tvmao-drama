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

// 五项画布配置写死为内置值（2026-10-07 用户要求）：白名单为空 = 全只读。
// 已有的 user_overrides 在合并时因找不到规则而作废（overlay），另见 apply.go 的 resetLockedSettings。
var settingPolicies = map[string]policy{
	SettingBaseDomain:         newPolicy(kindText, ruleDef{"$", checkBaseDomain, nil}),
	SettingCanvasLightControl: newPolicy(kindJSON),
	SettingCanvasMultiAngle:   newPolicy(kindJSON),
	SettingGenImagePresets:    newPolicy(kindJSON),
	SettingCanvasPostprocess:  newPolicy(kindJSON),
	SettingCanvasScript:       newPolicy(kindJSON),
}

// locked 报告该配置是否没有任何可改字段（写死为内置值）。
func (pl policy) locked() bool { return len(pl.rules) == 0 }

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
