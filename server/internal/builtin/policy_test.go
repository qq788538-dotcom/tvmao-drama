package builtin

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// 每个内置种子值都必须通过它自己的白名单校验，否则用户“原样保存”就会被拒绝；
// 同时保证每个种子配置都有 policy、JSON 配置确实是合法 JSON。
func TestSeedValuesPassTheirOwnRules(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cat := s.Catalog()
	for _, m := range s.Models {
		doc, err := modelDoc(m.parts())
		if err != nil {
			t.Fatalf("%s: %v", m.ModelID, err)
		}
		for _, r := range modelPolicy.rules {
			for _, p := range expandPattern(doc, r.pattern) {
				v, _ := getPath(doc, p)
				if err := r.check(doc, p, v, cat); err != nil {
					t.Errorf("模型 %s 的 %s 不通过校验：%v", m.ModelID, p, err)
				}
			}
		}
	}
	for _, st := range s.Settings {
		// base_domain 默认留空，由用户在设置页填写（去 qqdao 申请账号）。
		if st.Key == SettingBaseDomain && st.Value == "" {
			continue
		}
		pl, ok := settingPolicies[st.Key]
		if !ok {
			t.Errorf("种子配置 %s 没有 policy", st.Key)
			continue
		}
		doc, err := settingDoc(pl.kind, st.Value)
		if err != nil {
			t.Errorf("%s: %v", st.Key, err)
			continue
		}
		matched := 0
		for _, r := range pl.rules {
			for _, p := range expandPattern(doc, r.pattern) {
				matched++
				v, _ := getPath(doc, p)
				if err := r.check(doc, p, v, cat); err != nil {
					t.Errorf("配置 %s 的 %s 不通过校验：%v", st.Key, p, err)
				}
			}
		}
		if matched == 0 && !pl.locked() {
			t.Errorf("配置 %s 的白名单一个字段都没命中，模式写错了？", st.Key)
		}
	}
}

// 白名单模式不能互为前缀：否则同一份值会被两条覆盖记录同时管理，合并结果取决于顺序。
func TestPolicyPatternsDoNotNest(t *testing.T) {
	all := map[string]policy{"<model>": modelPolicy}
	for k, v := range settingPolicies {
		all[k] = v
	}
	for name, pl := range all {
		for i, a := range pl.rules {
			for j, b := range pl.rules {
				if i != j && len(a.pattern) < len(b.pattern) && a.pattern.Match(b.pattern[:len(a.pattern)]) {
					t.Errorf("%s：%s 是 %s 的前缀", name, a.pattern, b.pattern)
				}
			}
		}
	}
}

func modelTestDoc(t *testing.T) any {
	t.Helper()
	ui := `{"ratio":{"label":"比例","default":"1:1","options":[{"label":"1:1","value":"1:1"},{"label":"16:9","value":"16:9"}]},"duration":{"default":5,"options":[{"value":5},{"value":10}]}}`
	doc, err := modelDoc(modelParts{Name: "A", Enabled: true, UISchema: &ui})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func edits(kv ...string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = json.RawMessage(kv[i+1])
	}
	return out
}

func TestApplyEditsModelRules(t *testing.T) {
	doc := modelTestDoc(t)
	cases := []struct {
		name  string
		edits map[string]json.RawMessage
		bad   string // 期望报错的路径；空 = 期望成功
	}{
		{"改名", edits("name", `"新名字"`), ""},
		{"空名", edits("name", `"  "`), "name"},
		{"启用必须是布尔", edits("enabled", `1`), "enabled"},
		{"默认值在选项内", edits("ui_schema.ratio.default", `"16:9"`), ""},
		{"数字默认值按数值比较", edits("ui_schema.duration.default", `10.0`), ""},
		{"默认值不在选项内", edits("ui_schema.ratio.default", `"4:3"`), "ui_schema.ratio.default"},
		{"不存在的参数", edits("ui_schema.size.default", `"x"`), "ui_schema.size.default"},
		{"并发上限", edits("gen_control.concurrency", `201`), "gen_control.concurrency"},
		{"并发必须是整数", edits("gen_control.concurrency", `1.5`), "gen_control.concurrency"},
		{"gen_control 为 NULL 也能设重试", edits("gen_control.modules.canvas.maxRetries", `3`), ""},
		{"drama 模块不开放", edits("gen_control.modules.drama.maxRetries", `3`), "gen_control.modules.drama.maxRetries"},
		{"技术字段只读", edits("base_path", `"/x"`), "base_path"},
		{"请求模板只读", edits("request_template", `{}`), "request_template"},
		{"不能整体替换 uiSchema", edits("ui_schema", `{}`), "ui_schema"},
		{"非法 JSON", edits("name", `{`), "name"},
		{"非法路径", edits("a..b", `1`), "a..b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := applyEdits(modelPolicy, doc, doc, nil, c.edits, Catalog{})
			var ve *ValidationError
			if c.bad == "" {
				if err != nil {
					t.Fatalf("意外报错：%v", err)
				}
				return
			}
			if !errors.As(err, &ve) || ve.Path != c.bad {
				t.Fatalf("err=%v, want ValidationError on %s", err, c.bad)
			}
		})
	}
}

func TestApplyEditsTracksOverrides(t *testing.T) {
	seedDoc := modelTestDoc(t)
	doc, ov, err := applyEdits(modelPolicy, seedDoc, seedDoc, nil, edits("name", `"B"`, "ui_schema.ratio.default", `"16:9"`), Catalog{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ov, []string{"name", "ui_schema.ratio.default"}) {
		t.Fatalf("overrides=%v", ov)
	}
	// 改回与种子相同的值：覆盖记录移除，以后跟随种子。
	_, ov, err = applyEdits(modelPolicy, doc, seedDoc, ov, edits("name", `"A"`), Catalog{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ov, []string{"ui_schema.ratio.default"}) {
		t.Fatalf("overrides=%v", ov)
	}
}

// 一批编辑里只要有一项不合法，整批作废（调用方据此不写库）。
func TestApplyEditsIsAllOrNothing(t *testing.T) {
	doc := modelTestDoc(t)
	out, ov, err := applyEdits(modelPolicy, doc, doc, nil, edits("name", `"B"`, "base_path", `"/x"`), Catalog{})
	if err == nil || out != nil || ov != nil {
		t.Fatalf("out=%v ov=%v err=%v", out, ov, err)
	}
	if n, _ := getPath(doc, mustParsePath("name")); n != "A" {
		t.Fatal("applyEdits 不得修改传入的文档")
	}
}

func TestSettingRules(t *testing.T) {
	light := mustDecode(t, `{"models":["img"],"template":"打光：{light}","presets":[{"key":"p1","name":"P1","prompt":"a"}]}`)
	cases := []struct {
		name, key string
		doc       any
		edits     map[string]json.RawMessage
		bad       string
	}{
		{"写死的配置：提示词", SettingCanvasLightControl, light, edits("presets[p1].prompt", `"新的"`), "presets[p1].prompt"},
		{"写死的配置：模型列表", SettingCanvasLightControl, light, edits("models", `[]`), "models"},
		{"写死的配置：整体替换", SettingCanvasLightControl, light, edits("$", `{}`), "$"},
		{"上游地址", SettingBaseDomain, "http://a.example:3000", edits("$", `"https://gw.example.com"`), ""},
		{"上游地址不能带结尾 /", SettingBaseDomain, "http://a.example:3000", edits("$", `"https://gw.example.com/"`), "$"},
		{"上游地址协议", SettingBaseDomain, "http://a.example:3000", edits("$", `"ftp://gw.example.com"`), "$"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pl := settingPolicies[c.key]
			_, _, err := applyEdits(pl, c.doc, c.doc, nil, c.edits, Catalog{})
			var ve *ValidationError
			if c.bad == "" {
				if err != nil {
					t.Fatalf("意外报错：%v", err)
				}
				return
			}
			if !errors.As(err, &ve) || ve.Path != c.bad {
				t.Fatalf("err=%v, want ValidationError on %s", err, c.bad)
			}
		})
	}
}

// overlay 的通用行为（用模型白名单验证）：合法的覆盖保留，不在白名单内或不再通过校验的丢弃。
func TestOverlayKeepsValidAndDropsInvalid(t *testing.T) {
	ui := func(def string) *string {
		s := `{"ratio":{"default":"` + def + `","options":[{"value":"1:1"},{"value":"16:9"}]}}`
		return &s
	}
	base, err := modelDoc(modelParts{Name: "新名", Enabled: true, UISchema: ui("1:1")})
	if err != nil {
		t.Fatal(err)
	}
	user, err := modelDoc(modelParts{Name: "我的", Enabled: true, UISchema: ui("4:3")})
	if err != nil {
		t.Fatal(err)
	}
	doc, kept, dropped := overlay(modelPolicy, base, user, []string{"name", "ui_schema.ratio.default", "base_path"}, Catalog{})
	if !reflect.DeepEqual(kept, []string{"name"}) || !reflect.DeepEqual(dropped, []string{"ui_schema.ratio.default", "base_path"}) {
		t.Fatalf("kept=%v dropped=%v", kept, dropped)
	}
	if n, _ := getPath(doc, mustParsePath("name")); n != "我的" {
		t.Fatalf("name=%v", n)
	}
	if d, _ := getPath(doc, mustParsePath("ui_schema.ratio.default")); d != "1:1" {
		t.Fatalf("default=%v", d)
	}
}

func TestPartsFromDocKeepsSeedTextWhenNotOverridden(t *testing.T) {
	ui := `{"ratio": {"default": "1:1", "options": [{"value": "1:1"}, {"value": "16:9"}]}}`
	gc := `{"concurrency": 5}`
	seed := modelParts{Name: "A", Enabled: true, SortOrder: 3, UISchema: &ui, GenControl: &gc}
	doc, err := modelDoc(seed)
	if err != nil {
		t.Fatal(err)
	}
	doc, ov, err := applyEdits(modelPolicy, doc, doc, nil, edits("hidden", `true`, "gen_control.concurrency", `9`), Catalog{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := partsFromDoc(doc, seed, ov)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Hidden || p.Name != "A" || p.SortOrder != 3 {
		t.Fatalf("parts=%+v", p)
	}
	if *p.UISchema != ui {
		t.Fatalf("未覆盖的 ui_schema 必须保留种子原文，got %s", *p.UISchema)
	}
	if !strings.Contains(*p.GenControl, `"concurrency":9`) {
		t.Fatalf("gen_control=%s", *p.GenControl)
	}
}

func TestSettingValueFromDoc(t *testing.T) {
	seed := `{"models": ["img"], "template": "t {light}"}`
	if v, _ := settingValueFromDoc(kindJSON, seed, nil, nil); v != seed {
		t.Fatal("没有覆盖项时必须原样返回种子值")
	}
	v, err := settingValueFromDoc(kindText, "http://a", "https://b", []string{"$"})
	if err != nil || v != "https://b" {
		t.Fatalf("v=%q err=%v", v, err)
	}
}
