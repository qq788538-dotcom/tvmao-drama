package canvasgen

import (
	"encoding/json"
	"testing"
)

func paramsEntry(t *testing.T) GenImagePresetEntry {
	t.Helper()
	var e GenImagePresetEntry
	err := json.Unmarshal([]byte(`{"key":"portrait","value":{
		"name":"人像质感调节","prompt":"人像质感基础正文",
		"params":[
			{"key":"skin","name":"皮肤","options":[
				{"key":"clear","name":"清透修饰","prompt":"清透皮肤片段"},
				{"key":"natural","name":"自然肤质","prompt":"自然肤质片段","default":true},
				{"key":"real","name":"真实肌理","prompt":"真实肌理片段"}]},
			{"key":"sharp","name":"锐度","options":[
				{"key":"soft","name":"柔焦","prompt":""},
				{"key":"std","name":"标准清晰","prompt":"标准清晰片段","default":true}]}
		]}}`), &e)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// 拼接规则：正文 + 各维度选中档位片段（按维度顺序，换行分隔）；
// 缺失/非法维度落默认档；片段为空的档位跳过（「无效果」档位合法）。
func TestComposePresetText(t *testing.T) {
	e := paramsEntry(t)
	cases := []struct {
		name     string
		selected map[string]string
		want     string
	}{
		{"全默认", nil, "人像质感基础正文\n自然肤质片段\n标准清晰片段"},
		{"显式选档", map[string]string{"skin": "real"}, "人像质感基础正文\n真实肌理片段\n标准清晰片段"},
		{"非法档位落默认", map[string]string{"skin": "nope"}, "人像质感基础正文\n自然肤质片段\n标准清晰片段"},
		{"空片段档位被跳过", map[string]string{"sharp": "soft"}, "人像质感基础正文\n自然肤质片段"},
		{"未知维度被忽略", map[string]string{"ghost": "x"}, "人像质感基础正文\n自然肤质片段\n标准清晰片段"},
	}
	for _, c := range cases {
		if got := e.ComposePresetText(c.selected); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// 无 params 的预设：ComposePresetText 退化为纯正文——存量预设行为零变化。
func TestComposePresetTextNoParams(t *testing.T) {
	var e GenImagePresetEntry
	_ = json.Unmarshal([]byte(`{"key":"a","value":{"name":"x","prompt":"正文"}}`), &e)
	if got := e.ComposePresetText(map[string]string{"skin": "real"}); got != "正文" {
		t.Fatalf("got %q", got)
	}
}

// 无显式 default 时第一个档位是默认档。
func TestComposePresetTextFirstOptionDefault(t *testing.T) {
	var e GenImagePresetEntry
	_ = json.Unmarshal([]byte(`{"key":"a","value":{"name":"x","prompt":"P",
		"params":[{"key":"d","name":"维度","options":[
			{"key":"one","name":"一","prompt":"片段一"},
			{"key":"two","name":"二","prompt":"片段二"}]}]}}`), &e)
	if got := e.ComposePresetText(nil); got != "P\n片段一" {
		t.Fatalf("got %q", got)
	}
}
