package builtin

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// keyOrder 记录参照 JSON（种子原文）里每一层对象的键顺序。通用值解出来是 map，
// 重新编码会按字母序排键；ui_schema 的参数顺序就是前端 pill 的顺序，必须按种子原文保序。
type keyOrder struct {
	keys     []string
	children map[string]*keyOrder
	elems    []*keyOrder          // 数组元素，按下标
	byKey    map[string]*keyOrder // 数组里带 "key" 属性的对象元素，按 key 对齐（预设按 key 寻址）
	elemKey  string               // 本对象的 "key" 属性值（供上层数组建 byKey）
}

func parseKeyOrder(raw string) (*keyOrder, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	o, err := readKeyOrder(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("JSON 后面有多余内容")
	}
	return o, nil
}

// readKeyOrder 读入下一个值；标量返回 nil。
func readKeyOrder(dec *json.Decoder) (*keyOrder, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil, nil
	}
	o := &keyOrder{}
	switch d {
	case '{':
		o.children = map[string]*keyOrder{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			k, _ := kt.(string)
			if k == "key" {
				// 预读 "key" 的值：只在它是字符串时记录。
				vt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				if vd, isDelim := vt.(json.Delim); isDelim {
					return nil, errors.New("不支持的 key 值: " + vd.String())
				}
				if s, isStr := vt.(string); isStr {
					o.elemKey = s
				}
				o.keys = append(o.keys, k)
				continue
			}
			child, err := readKeyOrder(dec)
			if err != nil {
				return nil, err
			}
			o.keys = append(o.keys, k)
			o.children[k] = child
		}
	case '[':
		o.byKey = map[string]*keyOrder{}
		for dec.More() {
			child, err := readKeyOrder(dec)
			if err != nil {
				return nil, err
			}
			o.elems = append(o.elems, child)
			if child != nil && child.elemKey != "" {
				o.byKey[child.elemKey] = child
			}
		}
	}
	if _, err := dec.Token(); err != nil { // 结束分隔符
		return nil, err
	}
	return o, nil
}

// encodeJSONLike 与 encodeJSON 输出相同的 JSON 值，但对象键按 ref（种子原文）里的顺序排：
// ref 里有的键按 ref 顺序在前，ref 里没有的键按字母序在后。ref 解析失败时退回 encodeJSON。
func encodeJSONLike(v any, ref string) (string, error) {
	o, err := parseKeyOrder(ref)
	if err != nil {
		return encodeJSON(v)
	}
	var buf bytes.Buffer
	if err := writeOrdered(&buf, v, o); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func writeOrdered(buf *bytes.Buffer, v any, o *keyOrder) error {
	switch x := v.(type) {
	case map[string]any:
		var keys []string
		seen := map[string]bool{}
		if o != nil {
			for _, k := range o.keys {
				if _, ok := x[k]; ok && !seen[k] {
					keys = append(keys, k)
					seen[k] = true
				}
			}
		}
		for _, k := range sortedKeys(x) {
			if !seen[k] {
				keys = append(keys, k)
			}
		}
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			ks, err := encodeJSON(k)
			if err != nil {
				return err
			}
			buf.WriteString(ks)
			buf.WriteByte(':')
			var child *keyOrder
			if o != nil {
				child = o.children[k]
			}
			if err := writeOrdered(buf, x[k], child); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			var child *keyOrder
			if o != nil {
				if m, ok := e.(map[string]any); ok {
					if k, _ := m["key"].(string); k != "" {
						child = o.byKey[k]
					}
				}
				if child == nil && i < len(o.elems) {
					child = o.elems[i]
				}
			}
			if err := writeOrdered(buf, e, child); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	default:
		s, err := encodeJSON(v)
		if err != nil {
			return err
		}
		buf.WriteString(s)
		return nil
	}
}
