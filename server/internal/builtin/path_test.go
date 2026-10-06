package builtin

import (
	"encoding/json"
	"reflect"
	"testing"
)

func mustDecode(t *testing.T, s string) any {
	t.Helper()
	v, err := decodeJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParsePathRoundTrip(t *testing.T) {
	for _, s := range []string{"$", "name", "ui_schema.ratio.default", "presets[720_panorama].prompt", "presets[*].value.params[*].options[*].prompt", "gen_control.modules.canvas.maxRetries"} {
		p, err := ParsePath(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if p.String() != s {
			t.Fatalf("round trip %q -> %q", s, p.String())
		}
	}
}

func TestParsePathRejectsMalformed(t *testing.T) {
	for _, s := range []string{"", "a..b", "a.b c", "presets[x", "presets[].prompt", "a[b]c]", "$.a", "a/b"} {
		if _, err := ParsePath(s); err == nil {
			t.Fatalf("%q 应当解析失败", s)
		}
	}
}

func TestPatternMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"ui_schema.*.default", "ui_schema.ratio.default", true},
		{"ui_schema.*.default", "ui_schema.ratio.options", false},
		{"presets[*].prompt", "presets[rembrandt].prompt", true},
		{"presets[*].prompt", "presets.prompt", false},
		{"presets[*].prompt", "presets[rembrandt].name", false},
		{"$", "$", true},
		{"models", "models.x", false},
	}
	for _, c := range cases {
		if got := mustParsePath(c.pattern).Match(mustParsePath(c.path)); got != c.want {
			t.Fatalf("%s ~ %s = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestGetPathKeyedAndMissing(t *testing.T) {
	doc := mustDecode(t, `{"presets":[{"key":"a","prompt":"pa"},{"key":"b","prompt":"pb"}],"x":{"y":1}}`)
	if v, ok := getPath(doc, mustParsePath("presets[b].prompt")); !ok || v != "pb" {
		t.Fatalf("got %v %v", v, ok)
	}
	if _, ok := getPath(doc, mustParsePath("presets[c].prompt")); ok {
		t.Fatal("不存在的 key 应当 ok=false")
	}
	if _, ok := getPath(doc, mustParsePath("x.y.z")); ok {
		t.Fatal("穿过非对象应当 ok=false")
	}
	if v, ok := getPath(doc, Path{}); !ok || !reflect.DeepEqual(v, doc) {
		t.Fatal("$ 应当返回整个文档")
	}
}

func TestSetPathAutovivifiesObjectsButNotArrayElements(t *testing.T) {
	var doc any = map[string]any{"gen_control": nil}
	doc, err := setPath(doc, mustParsePath("gen_control.modules.canvas.maxRetries"), json.Number("2"))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := getPath(doc, mustParsePath("gen_control.modules.canvas.maxRetries")); v != json.Number("2") {
		t.Fatalf("got %v", v)
	}
	presets := mustDecode(t, `{"presets":[{"key":"a","prompt":"pa"}]}`)
	if _, err := setPath(presets, mustParsePath("presets[new].prompt"), "x"); err == nil {
		t.Fatal("不能借 [key] 新增数组元素")
	}
	if _, err := setPath(presets, mustParsePath("presets[a]"), "x"); err == nil {
		t.Fatal("不能整体替换数组元素")
	}
	root, err := setPath("old", Path{}, "new")
	if err != nil || root != "new" {
		t.Fatalf("$ 应当替换整个值: %v %v", root, err)
	}
}

func TestExpandPattern(t *testing.T) {
	doc := mustDecode(t, `{"ui_schema":{"ratio":{"default":"1:1"},"duration":{"default":5}},"presets":[{"key":"a","value":{"params":[{"key":"skin","options":[{"key":"n","prompt":""},{"key":"r","prompt":"x"}]}]}},{"key":"b","value":{}}]}`)
	got := []string{}
	for _, p := range expandPattern(doc, mustParsePath("ui_schema.*.default")) {
		got = append(got, p.String())
	}
	if !reflect.DeepEqual(got, []string{"ui_schema.duration.default", "ui_schema.ratio.default"}) {
		t.Fatalf("got %v", got)
	}
	got = got[:0]
	for _, p := range expandPattern(doc, mustParsePath("presets[*].value.params[*].options[*].prompt")) {
		got = append(got, p.String())
	}
	if !reflect.DeepEqual(got, []string{"presets[a].value.params[skin].options[n].prompt", "presets[a].value.params[skin].options[r].prompt"}) {
		t.Fatalf("got %v", got)
	}
}

func TestJSONEqualAndEncoding(t *testing.T) {
	if !jsonEqual(mustDecode(t, `{"a":10,"b":[1,"x"]}`), mustDecode(t, `{"b":[1.0,"x"],"a":10.0}`)) {
		t.Fatal("数值相等、键顺序不同应当相等")
	}
	if jsonEqual(mustDecode(t, `"10"`), mustDecode(t, `10`)) {
		t.Fatal("字符串与数字不相等")
	}
	s, err := encodeJSON(mustDecode(t, `{"p":"a<b>&c","n":1.50}`))
	if err != nil || s != `{"n":1.50,"p":"a<b>&c"}` {
		t.Fatalf("encode=%s %v", s, err)
	}
	if _, err := decodeJSON([]byte(`{"a":1} x`)); err == nil {
		t.Fatal("多余内容应当报错")
	}
}
