package builtin

import (
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
		SettingBaseDomain: {"$"},
		// 五项画布配置写死（2026-10-07）：全只读。
		SettingCanvasLightControl: {},
		SettingCanvasMultiAngle:   {},
		SettingGenImagePresets:    {},
		SettingCanvasPostprocess:  {},
		SettingCanvasScript:       {},
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
