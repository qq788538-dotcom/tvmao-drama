package builtin

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// ---- I1：带 CASCADE 外键的子表（P5 的 agent_tools.source_model_id）不能让 Apply 失败 ----

const cascadeChildDDL = `CREATE TABLE child_tool (id INTEGER PRIMARY KEY, mid INTEGER NOT NULL REFERENCES model_configs(id) ON DELETE CASCADE ON UPDATE CASCADE)`

func TestApplyDeleteModelWithCascadeChild(t *testing.T) {
	conn := dbtest.Open(t)
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", nil), seedModelLine(t, 2, "img-b", "image", nil)}, nil))
	simulateUserEdit(t, conn, cascadeChildDDL)
	simulateUserEdit(t, conn, `INSERT INTO child_tool (id, mid) VALUES (10, 1), (11, 2)`)
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", nil)}, nil))
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM child_tool WHERE mid = 1`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("未删除模型的子行应保留：n=%d err=%v", n, err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM child_tool WHERE mid = 2`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("被删模型的子行应级联删除：n=%d err=%v", n, err)
	}
}

func TestApplyRenumberModelWithCascadeChildFollowsNewID(t *testing.T) {
	conn := dbtest.Open(t)
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", nil), seedModelLine(t, 2, "img-b", "image", nil)}, nil))
	simulateUserEdit(t, conn, cascadeChildDDL)
	simulateUserEdit(t, conn, `INSERT INTO child_tool (id, mid) VALUES (10, 1), (11, 2)`)
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 2, "img-a", "image", nil), seedModelLine(t, 1, "img-b", "image", nil)}, nil))
	// 子行必须跟着“改主键”走（说明用的是 UPDATE 而不是先删后插）：10 原属 img-a，现在 img-a 的 id 是 2。
	var mid int64
	if err := conn.QueryRow(`SELECT mid FROM child_tool WHERE id = 10`).Scan(&mid); err != nil || mid != 2 {
		t.Fatalf("mid=%d err=%v，want 2", mid, err)
	}
	if err := conn.QueryRow(`SELECT mid FROM child_tool WHERE id = 11`).Scan(&mid); err != nil || mid != 1 {
		t.Fatalf("mid=%d err=%v，want 1", mid, err)
	}
}

// ---- M2：坏的 user_overrides 不能阻断启动 ----

func TestApplyToleratesMalformedUserOverrides(t *testing.T) {
	conn := dbtest.Open(t)
	models := []string{seedModelLine(t, 1, "img-a", "image", nil)}
	st := lightSetting(t, `["img-a"]`, "{light}", `[]`)
	mustApply(t, conn, mustSeed(t, models, []string{st}))
	simulateUserEdit(t, conn, `UPDATE model_configs SET name = '我的', user_overrides = 'x' WHERE model_id = 'img-a'`)
	simulateUserEdit(t, conn, `UPDATE settings SET user_overrides = '{"a":1}' WHERE "key" = ?`, SettingCanvasLightControl)
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"name": "新"})}, []string{st}))
	r := modelByID(t, conn, "img-a")
	if r.Parts.Name != "新" || r.Overrides != nil {
		t.Fatalf("坏覆盖应视为空并跟随新版：%+v", r)
	}
	if got := settingByKey(t, conn, SettingCanvasLightControl); got.Overrides != nil {
		t.Fatalf("overrides=%v", got.Overrides)
	}
}

// ---- M3：只剔除下线的模型，保留用户列表的其余部分 ----

func TestApplyListOverridePrunesOnlyRemovedModels(t *testing.T) {
	conn := dbtest.Open(t)
	three := []string{seedModelLine(t, 1, "img-a", "image", nil), seedModelLine(t, 2, "img-b", "image", nil), seedModelLine(t, 3, "img-c", "image", nil)}
	mustApply(t, conn, mustSeed(t, three, []string{lightSetting(t, `["img-a"]`, "{light}", `[]`)}))
	simulateUserEdit(t, conn, `UPDATE settings SET value = '{"models":["img-b","img-a","img-c"],"presets":[],"template":"{light}"}', user_overrides = '["models"]' WHERE "key" = ?`, SettingCanvasLightControl)
	mustApply(t, conn, mustSeed(t, three[:2], []string{lightSetting(t, `["img-a"]`, "{light}", `[]`)}))
	got := settingByKey(t, conn, SettingCanvasLightControl)
	if want := `{"models":["img-b","img-a"],"presets":[],"template":"{light}"}`; got.Value != want || !reflect.DeepEqual(got.Overrides, []string{"models"}) {
		t.Fatalf("got %s %v", got.Value, got.Overrides)
	}
}

// 用户列表里的模型全部下线：剔除后为空等于把功能关掉，不是用户本意，回到新版默认。
func TestApplyListOverrideAllRemovedFallsBackToSeed(t *testing.T) {
	conn := dbtest.Open(t)
	two := []string{seedModelLine(t, 1, "img-a", "image", nil), seedModelLine(t, 2, "img-b", "image", nil)}
	mustApply(t, conn, mustSeed(t, two, []string{lightSetting(t, `["img-a"]`, "{light}", `[]`)}))
	simulateUserEdit(t, conn, `UPDATE settings SET value = '{"models":["img-b"],"presets":[],"template":"{light}"}', user_overrides = '["models"]' WHERE "key" = ?`, SettingCanvasLightControl)
	mustApply(t, conn, mustSeed(t, two[:1], []string{lightSetting(t, `["img-a"]`, "{light}", `[]`)}))
	got := settingByKey(t, conn, SettingCanvasLightControl)
	if want := `{"models":["img-a"],"template":"{light}","presets":[]}`; got.Value != want || got.Overrides != nil {
		t.Fatalf("got %s %v", got.Value, got.Overrides)
	}
}

// 去字幕列表：用户改了 B 的 label，新版下线了 C → B 的 label 保留，C 随之消失。
func TestApplyDesubOverrideFollowsSeedStructure(t *testing.T) {
	conn := dbtest.Open(t)
	vids := []string{seedModelLine(t, 1, "v-a", "video", nil), seedModelLine(t, 2, "v-b", "video", nil), seedModelLine(t, 3, "v-c", "video", nil)}
	post := func(items string) string {
		return seedSettingLine(t, SettingCanvasPostprocess, `{"subtitleRemovalModels":`+items+`,"upscaleModel":""}`)
	}
	item := func(id, label string) string {
		return `{"modelId":"` + id + `","label":"` + label + `","subtitle":"","isTimeLimited":false}`
	}
	mustApply(t, conn, mustSeed(t, vids, []string{post(`[` + item("v-a", "A") + `,` + item("v-b", "B") + `,` + item("v-c", "C") + `]`)}))
	simulateUserEdit(t, conn, `UPDATE settings SET value = ?, user_overrides = '["subtitleRemovalModels"]' WHERE "key" = ?`,
		`{"subtitleRemovalModels":[`+item("v-a", "A")+`,`+item("v-b", "我的B")+`,`+item("v-c", "C")+`],"upscaleModel":""}`, SettingCanvasPostprocess)
	mustApply(t, conn, mustSeed(t, vids[:2], []string{post(`[` + item("v-a", "A") + `,` + item("v-b", "B") + `]`)}))
	got := settingByKey(t, conn, SettingCanvasPostprocess)
	// 覆盖后整列按 Go 键序重新编码（已知，见终审 M8），所以按 JSON 语义比较。
	want := mustDecode(t, `{"subtitleRemovalModels":[`+item("v-a", "A")+`,`+item("v-b", "我的B")+`],"upscaleModel":""}`)
	if !jsonEqual(mustDecode(t, got.Value), want) {
		t.Fatalf("got %s", got.Value)
	}
}

// ---- M4：列表型条目不能增删、不能改 id/model，只能改展示与开关字段 ----

func TestListRulesForbidAddRemoveReorderAndIDChange(t *testing.T) {
	cat := Catalog{"vid": "video", "vid2": "video", "txt": "text", "txt2": "text"}
	post := mustDecode(t, `{"subtitleRemovalModels":[{"modelId":"vid","label":"标准","subtitle":"","isTimeLimited":false},{"modelId":"vid2","label":"B"}],"upscaleModel":"vid"}`)
	script := mustDecode(t, `{"modelCandidates":[{"modelId":"txt","displayName":"T"},{"modelId":"txt2","displayName":"T2"}],"concurrency":5}`)
	cases := []struct {
		name, key string
		doc       any
		path      string
		val       string
		bad       bool
	}{
		{"去字幕：改展示字段", SettingCanvasPostprocess, post, "subtitleRemovalModels", `[{"modelId":"vid","label":"L","subtitle":"s","isTimeLimited":true},{"modelId":"vid2","label":"B"}]`, false},
		{"去字幕：不能删条目", SettingCanvasPostprocess, post, "subtitleRemovalModels", `[{"modelId":"vid","label":"标准"}]`, true},
		{"去字幕：不能增条目", SettingCanvasPostprocess, post, "subtitleRemovalModels", `[{"modelId":"vid","label":"a"},{"modelId":"vid2","label":"B"},{"modelId":"vid2","label":"c"}]`, true},
		{"去字幕：不能换 modelId", SettingCanvasPostprocess, post, "subtitleRemovalModels", `[{"modelId":"vid2","label":"a"},{"modelId":"vid2","label":"B"}]`, true},
		{"去字幕：不能调换顺序", SettingCanvasPostprocess, post, "subtitleRemovalModels", `[{"modelId":"vid2","label":"B"},{"modelId":"vid","label":"a"}]`, true},
		{"脚本候选：改 displayName", SettingCanvasScript, script, "modelCandidates", `[{"modelId":"txt","displayName":"X"},{"modelId":"txt2","displayName":"Y"}]`, false},
		{"脚本候选：不能删", SettingCanvasScript, script, "modelCandidates", `[{"modelId":"txt","displayName":"X"}]`, true},
		{"脚本候选：不能清空", SettingCanvasScript, script, "modelCandidates", `[]`, true},
		{"脚本候选：不能换 modelId", SettingCanvasScript, script, "modelCandidates", `[{"modelId":"txt2","displayName":"X"},{"modelId":"txt2","displayName":"Y"}]`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := applyEdits(settingPolicies[c.key], c.doc, c.doc, nil, map[string]json.RawMessage{c.path: json.RawMessage(c.val)}, cat)
			var ve *ValidationError
			if !c.bad {
				if err != nil {
					t.Fatalf("意外报错：%v", err)
				}
				return
			}
			if !errors.As(err, &ve) || ve.Path != c.path {
				t.Fatalf("err=%v，want ValidationError on %s", err, c.path)
			}
		})
	}
}

// ---- M9：用户值与新版种子值相同时，覆盖记录退回“跟随内置” ----

func TestOverlayDropsOverrideEqualToNewSeed(t *testing.T) {
	conn := dbtest.Open(t)
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"name": "A1"})}, nil))
	simulateUserEdit(t, conn, `UPDATE model_configs SET name = 'A2', user_overrides = '["name"]' WHERE model_id = 'img-a'`)
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"name": "A2"})}, nil))
	if r := modelByID(t, conn, "img-a"); r.Overrides != nil || r.Parts.Name != "A2" {
		t.Fatalf("%+v", r)
	}
}

// ---- M5：白名单一变就逼开发者递增 seedFormatVersion ----

func TestPolicyPatternsSnapshot(t *testing.T) {
	got := map[string][]string{"<model>": modelPolicy.Patterns()}
	for k, pl := range settingPolicies {
		got[k] = pl.Patterns()
	}
	want := map[string][]string{
		"<model>": {"name", "enabled", "hidden", "sort_order", "ui_schema.*.default", "gen_control.concurrency",
			"gen_control.modules.canvas.maxRetries", "gen_control.modules.canvas.retryIntervalSec"},
		SettingBaseDomain:         {"$"},
		SettingCanvasLightControl: {"models", "template", "presets[*].prompt"},
		SettingCanvasMultiAngle:   {"models", "template", "presets[*].prompt"},
		SettingGenImagePresets:    {"models", "presets[*].value.prompt", "presets[*].value.params[*].options[*].prompt"},
		SettingCanvasPostprocess:  {"subtitleRemovalModels", "upscaleModel"},
		SettingCanvasScript:       {"modelCandidates", "concurrency", "maxRetries"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("可改字段白名单变了：请递增 seed.go 的 seedFormatVersion（当前 %s），再更新本快照。\ngot  %v\nwant %v", seedFormatVersion, got, want)
	}
}

// ---- M7：结尾的 ? / # ----

func TestCheckBaseDomainRejectsDanglingQueryOrFragment(t *testing.T) {
	for _, s := range []string{"http://h:3000?", "http://h:3000#", "http://h:3000/?", "http://h:3000?a=1"} {
		if err := checkBaseDomain(nil, nil, s, nil); err == nil {
			t.Errorf("%q 应被拒绝", s)
		}
	}
	if err := checkBaseDomain(nil, nil, "https://h.example:3000", nil); err != nil {
		t.Error(err)
	}
}

// ---- M10：decodeStrict 的尾随垃圾、secretKeyRe 的误伤 ----

func TestDecodeStrictRejectsTrailingGarbage(t *testing.T) {
	for _, s := range []string{`{"a":1}]`, `{"a":1}}`, `{"a":1} {"a":2}`, `{"a":1} x`} {
		var v struct{ A int }
		if err := decodeStrict([]byte(s), &v); err == nil {
			t.Errorf("%q 应被拒绝", s)
		}
	}
}

func TestSecretKeyRe(t *testing.T) {
	for _, k := range []string{"upstream_api_key", "access_token", "token", "client_secret", "db_password"} {
		if !secretKeyRe.MatchString(k) {
			t.Errorf("%q 应判为密钥", k)
		}
	}
	for _, k := range []string{"max_tokens", "maxTokens", "tokenizer", "base_domain"} {
		if secretKeyRe.MatchString(k) {
			t.Errorf("%q 不应判为密钥", k)
		}
	}
}

// ---- M11：expandPattern → ParsePath 往返 ----

func TestExpandedPathsRoundTripThroughParsePath(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	check := func(who string, pl policy, doc any) {
		for _, r := range pl.rules {
			for _, p := range expandPattern(doc, r.pattern) {
				back, err := ParsePath(p.String())
				if err != nil || back.String() != p.String() {
					t.Errorf("%s：%s 无法往返 ParsePath（%v）", who, p, err)
				}
				if _, ok := getPath(doc, back); !ok {
					t.Errorf("%s：%s 往返后取不到值", who, p)
				}
			}
		}
	}
	for _, m := range s.Models {
		doc, err := modelDoc(m.parts())
		if err != nil {
			t.Fatal(err)
		}
		check(m.ModelID, modelPolicy, doc)
	}
	for _, st := range s.Settings {
		pl := settingPolicies[st.Key]
		doc, err := settingDoc(pl.kind, st.Value)
		if err != nil {
			t.Fatal(err)
		}
		check(st.Key, pl, doc)
	}
}
