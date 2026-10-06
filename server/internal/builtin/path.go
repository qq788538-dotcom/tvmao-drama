package builtin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// 字段路径：user_overrides 里存的、编辑接口收的、白名单里写的都是它的规范字符串。
//
//	path := "$" | seg ("." seg)*
//	seg  := name | name "[" key "]"
//
// name、key 只含 [A-Za-z0-9_-]；白名单模式里可以写 "*" 通配。
// name 取对象字段；name[key] 取数组字段里 "key" 属性等于 key 的元素——预设按 key 寻址而不是下标，
// 新版种子调整预设顺序或插入新预设时，用户对某条预设的修改不会错位。
// "$" 表示整个值（base_domain 这种纯文本配置）。

type segment struct {
	Name  string
	Key   string
	Keyed bool
}

// Path 是解析后的字段路径；空切片表示 "$"。
type Path []segment

var identRe = regexp.MustCompile(`^(\*|[A-Za-z0-9_-]+)$`)

// ParsePath 解析字段路径字符串。
func ParsePath(s string) (Path, error) {
	if s == "$" {
		return Path{}, nil
	}
	if s == "" {
		return nil, errors.New("字段路径为空")
	}
	parts := strings.Split(s, ".")
	out := make(Path, 0, len(parts))
	for _, part := range parts {
		seg := segment{Name: part}
		if i := strings.IndexByte(part, '['); i >= 0 {
			if !strings.HasSuffix(part, "]") {
				return nil, fmt.Errorf("字段路径 %q：%q 缺少 ]", s, part)
			}
			seg = segment{Name: part[:i], Key: part[i+1 : len(part)-1], Keyed: true}
			if !identRe.MatchString(seg.Key) {
				return nil, fmt.Errorf("字段路径 %q：%q 的 key 不合法", s, part)
			}
		}
		if !identRe.MatchString(seg.Name) {
			return nil, fmt.Errorf("字段路径 %q：%q 不合法", s, part)
		}
		out = append(out, seg)
	}
	return out, nil
}

func mustParsePath(s string) Path {
	p, err := ParsePath(s)
	if err != nil {
		panic(err)
	}
	return p
}

// String 返回规范字符串，与 ParsePath 互逆。
func (p Path) String() string {
	if len(p) == 0 {
		return "$"
	}
	var b strings.Builder
	for i, s := range p {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s.Name)
		if s.Keyed {
			b.WriteString("[" + s.Key + "]")
		}
	}
	return b.String()
}

// Match 判断具体路径 p 是否命中模式 pattern：段数相同，逐段比较，"*" 匹配任意 name 或 key。
func (pattern Path) Match(p Path) bool {
	if len(pattern) != len(p) {
		return false
	}
	for i := range pattern {
		a, b := pattern[i], p[i]
		if a.Keyed != b.Keyed {
			return false
		}
		if a.Name != "*" && a.Name != b.Name {
			return false
		}
		if a.Keyed && a.Key != "*" && a.Key != b.Key {
			return false
		}
	}
	return true
}

// findKeyed 在数组 v 里找 "key" 属性等于 key 的对象元素。
func findKeyed(v any, key string) (map[string]any, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			if k, _ := m["key"].(string); k == key {
				return m, true
			}
		}
	}
	return nil, false
}

// getPath 取 doc 上路径 p 处的值；任一段不存在返回 ok=false。
func getPath(doc any, p Path) (any, bool) {
	cur := doc
	for _, s := range p {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[s.Name]
		if !ok {
			return nil, false
		}
		if s.Keyed {
			elem, ok := findKeyed(v, s.Key)
			if !ok {
				return nil, false
			}
			v = elem
		}
		cur = v
	}
	return cur, true
}

// setPath 把 doc 上路径 p 处的值设为 v，返回新的根（p 为 "$" 时根被替换为 v）。
// 中途缺失或为 null 的对象字段自动建成空对象（gen_control 为 NULL 的模型也能设并发）；
// 带 [key] 的段必须命中已有元素——用户不能借此新增预设。会原地修改 doc，调用方先 deepCopy。
func setPath(doc any, p Path, v any) (any, error) {
	if len(p) == 0 {
		return v, nil
	}
	if doc == nil {
		doc = map[string]any{}
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s 的上一级不是对象", p)
	}
	s := p[0]
	if s.Keyed {
		elem, ok := findKeyed(m[s.Name], s.Key)
		if !ok {
			return nil, fmt.Errorf("找不到 %s[%s]", s.Name, s.Key)
		}
		if len(p) == 1 {
			return nil, fmt.Errorf("不能整体替换 %s[%s]", s.Name, s.Key)
		}
		if _, err := setPath(elem, p[1:], v); err != nil {
			return nil, err
		}
		return m, nil
	}
	if len(p) == 1 {
		m[s.Name] = v
		return m, nil
	}
	child, err := setPath(m[s.Name], p[1:], v)
	if err != nil {
		return nil, err
	}
	m[s.Name] = child
	return m, nil
}

// expandPattern 列出 doc 上实际存在、且命中模式 pattern 的全部具体路径（顺序与文档顺序一致）。
func expandPattern(doc any, pattern Path) []Path {
	var out []Path
	var walk func(cur any, i int, prefix Path)
	walk = func(cur any, i int, prefix Path) {
		if i == len(pattern) {
			out = append(out, append(Path{}, prefix...))
			return
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return
		}
		seg := pattern[i]
		var names []string
		if seg.Name == "*" {
			names = sortedKeys(m)
		} else if _, ok := m[seg.Name]; ok {
			names = []string{seg.Name}
		}
		for _, name := range names {
			v := m[name]
			if !seg.Keyed {
				walk(v, i+1, append(prefix, segment{Name: name}))
				continue
			}
			arr, _ := v.([]any)
			for _, e := range arr {
				em, ok := e.(map[string]any)
				if !ok {
					continue
				}
				k, _ := em["key"].(string)
				if k == "" || (seg.Key != "*" && seg.Key != k) {
					continue
				}
				walk(em, i+1, append(prefix, segment{Name: name, Key: k, Keyed: true}))
			}
		}
	}
	walk(doc, 0, Path{})
	return out
}

// decodeJSON 把一段 JSON 解成通用值；数字保持 json.Number，重新编码时不丢精度、不改写法。
func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("JSON 后面有多余内容")
	}
	return v, nil
}

// encodeJSON 编码通用值；不转义 <>&（提示词里常见），不带结尾换行。
func encodeJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

func deepCopy(v any) (any, error) {
	s, err := encodeJSON(v)
	if err != nil {
		return nil, err
	}
	return decodeJSON([]byte(s))
}

// jsonEqual 按 JSON 语义比较：数字按数值比（10 与 10.0 相等），对象不看键顺序。
func jsonEqual(a, b any) bool {
	return reflect.DeepEqual(normalizeNumbers(a), normalizeNumbers(b))
}

func normalizeNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case int:
		return float64(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalizeNumbers(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalizeNumbers(e)
		}
		return out
	default:
		return v
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
