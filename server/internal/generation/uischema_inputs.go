package generation

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// normalizeInputsToUISchema 把 inputs 里「uiSchema 定义了枚举 options 的字段」归一化
// 成 option value 的原值（连同 JSON 类型）。
//
// 为什么放在提交边界：各写入方对枚举值的类型把握不一——LLM 分镜提取会把 duration
// 写成数字 4，而模型请求模板期望字符串 "4"（上游 seconds 是 string，收到 JSON 数字
// 直接 400 invalid_json）。uiSchema 是合法值的唯一真源，且只有提交层同时拿得到
// 模型 schema 和最终 inputs，在这里按字符串形式对得上就以 option value 为准，
// 能一次兜住所有写入方和存量脏数据。
//
// 对不上任何 option 的值原样保留——不是所有字段都会把合法值枚举完整（自由输入）。
func normalizeInputsToUISchema(uiSchema json.RawMessage, inputs map[string]any) map[string]any {
	if len(uiSchema) == 0 || len(inputs) == 0 {
		return inputs
	}
	var schema map[string]struct {
		Options []struct {
			Value any `json:"value"`
		} `json:"options"`
	}
	if err := json.Unmarshal(uiSchema, &schema); err != nil {
		return inputs
	}

	out := make(map[string]any, len(inputs))
	for k, v := range inputs {
		out[k] = v
	}
	for key, field := range schema {
		cur, ok := out[key]
		if !ok || len(field.Options) == 0 {
			continue
		}
		curStr, ok := enumString(cur)
		if !ok {
			continue
		}
		for _, opt := range field.Options {
			optStr, ok := enumString(opt.Value)
			if ok && optStr == curStr {
				out[key] = opt.Value
				break
			}
		}
	}
	return out
}

// validateInputsAgainstUISchema 对声明了 options 的字段做白名单校验:inputs 提供了该字段、
// 但值(按字符串形态)不在 options 内 → 报错。防客户端注入越界计费字段(如 duration=1)
// 薅按秒计费。未提供的字段、未声明 options 的字段一律放行。
func validateInputsAgainstUISchema(uiSchema json.RawMessage, inputs map[string]any) error {
	if len(uiSchema) == 0 || len(inputs) == 0 {
		return nil
	}
	var schema map[string]struct {
		Options []struct {
			Value any `json:"value"`
		} `json:"options"`
	}
	if err := json.Unmarshal(uiSchema, &schema); err != nil {
		return nil // schema 不可解析不阻断(与 normalize 一致的宽容)
	}
	for field, def := range schema {
		if len(def.Options) == 0 {
			continue
		}
		v, ok := inputs[field]
		if !ok {
			continue
		}
		vs, ok := enumString(v)
		if !ok {
			continue // 非标量(对象/数组)不在枚举校验范围
		}
		allowed := false
		for _, opt := range def.Options {
			if os, ok := enumString(opt.Value); ok && os == vs {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("字段 %q 的值 %q 不在允许范围内", field, vs)
		}
	}
	return nil
}

// enumString 给枚举比对用的稳定字符串形式。只处理 JSON 标量里适合做枚举的
// string/number；其余类型（bool/对象/数组/null）不参与归一化。
func enumString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case json.Number:
		return t.String(), true
	default:
		return "", false
	}
}
