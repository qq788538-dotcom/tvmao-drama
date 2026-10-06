package assets

import "encoding/json"

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
func TransformJSONRaw(raw json.RawMessage, transform func(string) string) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
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
