package builtin

import (
	"testing"
)

// 只改了一个参数的 default，ui_schema 整列重新编码时键序必须跟种子原文一致，
// 前端按对象键序渲染的 pill 顺序不能变成字母序。
func TestPartsFromDocKeepsSeedKeyOrder(t *testing.T) {
	ui := `{"ratio":{"label":"比例","default":"1:1","options":[{"value":"1:1","label":"方"},{"value":"16:9","label":"宽"}]},"duration":{"label":"时长","default":"5","options":[{"value":"5"},{"value":"10"}]}}`
	seed := modelParts{Name: "A", Enabled: true, UISchema: &ui}
	doc, err := modelDoc(seed)
	if err != nil {
		t.Fatal(err)
	}
	doc, ov, err := applyEdits(modelPolicy, doc, doc, nil, edits("ui_schema.ratio.default", `"16:9"`), Catalog{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := partsFromDoc(doc, seed, ov)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ratio":{"label":"比例","default":"16:9","options":[{"value":"1:1","label":"方"},{"value":"16:9","label":"宽"}]},"duration":{"label":"时长","default":"5","options":[{"value":"5"},{"value":"10"}]}}`
	if *p.UISchema != want {
		t.Fatalf("got  %s\nwant %s", *p.UISchema, want)
	}
}

func TestEncodeJSONLike(t *testing.T) {
	ref := `{"b":1,"a":{"z":1,"y":2},"list":[{"key":"k2","q":1,"p":2},{"key":"k1","n":1,"m":2}]}`
	v, err := decodeJSON([]byte(`{"a":{"y":2,"z":1,"x":0},"c":3,"b":1,"list":[{"key":"k1","m":2,"n":1},{"key":"k2","p":2,"q":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := encodeJSONLike(v, ref)
	if err != nil {
		t.Fatal(err)
	}
	// 种子里有的键按种子顺序，新键按字母序排在后面；数组元素按 key 属性对齐种子元素。
	want := `{"b":1,"a":{"z":1,"y":2,"x":0},"list":[{"key":"k1","n":1,"m":2},{"key":"k2","q":1,"p":2}],"c":3}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// 参照文本不是合法 JSON 时退回普通编码。
	if got, err := encodeJSONLike(v, "not json"); err != nil || got == "" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
