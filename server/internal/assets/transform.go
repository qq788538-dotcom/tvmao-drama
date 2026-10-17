package assets

import (
	"bytes"
	"encoding/json"
	"io"
)

// WalkStrings 遍历任意 JSON 值（string / map / slice），对每个 string 叶子节点
// 应用 transform，返回一棵新树。
func WalkStrings(v any, transform func(string) string) any {
	switch x := v.(type) {
	case string:
		return transform(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = WalkStrings(val, transform)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = WalkStrings(val, transform)
		}
		return out
	default:
		return v
	}
}

// TransformJSONRaw 对一个 json.RawMessage 做解码、遍历、重新编码。
// 如果输入为空或无法解析，则原样返回原始字节。
// 数字按 json.Number 原文保留：解成 float64 会把超过 2^53 的整数（id、seed）改掉后写回库里。
func TransformJSONRaw(raw json.RawMessage, transform func(string) string) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return raw
	}
	// 与 json.Unmarshal 一致：尾随多余内容视为无法解析。
	if _, err := dec.Token(); err != io.EOF {
		return raw
	}
	out := WalkStrings(decoded, transform)
	encoded, err := json.Marshal(out)
	if err != nil {
		return raw
	}
	return encoded
}

// TransformJSONBytes 是面向 []byte 的便捷封装。
func TransformJSONBytes(raw []byte, transform func(string) string) []byte {
	return TransformJSONRaw(raw, transform)
}
