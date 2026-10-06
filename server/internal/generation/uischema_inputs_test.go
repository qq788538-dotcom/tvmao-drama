package generation

import (
	"encoding/json"
	"testing"
)

// 复现线上 bug：LLM 提取把 duration 写成数字 4，而模型 uiSchema 的 option value
// 是字符串 "4"（上游 seconds 是 string 类型）——提交层必须按 schema 归一化。
func TestNormalizeInputsToUISchema(t *testing.T) {
	uiSchema := json.RawMessage(`{
		"duration": {"label": "时长", "default": "15", "options": [
			{"label": "4s", "value": "4"}, {"label": "5s", "value": "5"}
		]},
		"n": {"label": "数量", "options": [
			{"label": "1", "value": 1}, {"label": "2", "value": 2}
		]},
		"prompt_hint": {"label": "无options字段"}
	}`)

	cases := []struct {
		name   string
		inputs map[string]any
		key    string
		want   any
	}{
		{
			name:   "数字对上字符串option→取option的字符串值",
			inputs: map[string]any{"duration": float64(4)},
			key:    "duration",
			want:   "4",
		},
		{
			name:   "字符串对上数字option→取option的数字值",
			inputs: map[string]any{"n": "2"},
			key:    "n",
			want:   float64(2),
		},
		{
			name:   "已是规范值→原样",
			inputs: map[string]any{"duration": "5"},
			key:    "duration",
			want:   "5",
		},
		{
			name:   "对不上任何option→原样保留(自由输入)",
			inputs: map[string]any{"duration": float64(7)},
			key:    "duration",
			want:   float64(7),
		},
		{
			name:   "schema里没有的key→不动",
			inputs: map[string]any{"prompt": "海边散步"},
			key:    "prompt",
			want:   "海边散步",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeInputsToUISchema(uiSchema, tc.inputs)
			if got[tc.key] != tc.want {
				t.Fatalf("got %#v (%T), want %#v (%T)", got[tc.key], got[tc.key], tc.want, tc.want)
			}
		})
	}
}

func TestNormalizeInputsToUISchemaNilSchema(t *testing.T) {
	in := map[string]any{"duration": float64(4)}
	got := normalizeInputsToUISchema(nil, in)
	if got["duration"] != float64(4) {
		t.Fatalf("nil schema 应原样返回, got %#v", got["duration"])
	}
}

func TestNormalizeInputsToUISchemaDoesNotMutateCaller(t *testing.T) {
	uiSchema := json.RawMessage(`{"duration": {"options": [{"label": "4s", "value": "4"}]}}`)
	in := map[string]any{"duration": float64(4)}
	_ = normalizeInputsToUISchema(uiSchema, in)
	if in["duration"] != float64(4) {
		t.Fatalf("不应修改调用方的 map, got %#v", in["duration"])
	}
}

func TestValidateInputsAgainstUISchema(t *testing.T) {
	schema := json.RawMessage(`{"duration":{"options":[{"value":5},{"value":10}]},"resolution":{"options":[{"value":"720p"},{"value":"1080p"}]}}`)
	cases := []struct {
		name    string
		inputs  map[string]any
		wantErr bool
	}{
		{"合法枚举值", map[string]any{"duration": 5, "resolution": "720p"}, false},
		{"字符串形态匹配数字选项", map[string]any{"duration": "10"}, false},
		{"duration 越界(注入 1)", map[string]any{"duration": 1}, true},
		{"resolution 越界", map[string]any{"resolution": "8k"}, true},
		{"未提供该字段不报错", map[string]any{"resolution": "720p"}, false},
		{"schema 未声明 options 的字段放行", map[string]any{"prompt": "任意文本"}, false},
	}
	for _, c := range cases {
		err := validateInputsAgainstUISchema(schema, c.inputs)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
	}
}
