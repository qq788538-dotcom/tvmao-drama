package builtin

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func mustApply(t *testing.T, conn *sql.DB, s *Seed) {
	t.Helper()
	if err := applySeed(context.Background(), conn, s); err != nil {
		t.Fatal(err)
	}
}

// simulateUserEdit 模拟用户经 Service 改过字段后的库状态（Service 属于另一个任务，这里直接写库）。
func simulateUserEdit(t *testing.T, conn *sql.DB, sqlText string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(sqlText, args...); err != nil {
		t.Fatal(err)
	}
}

func TestApplyFirstLaunchImportsEmbeddedSeed(t *testing.T) {
	conn := dbtest.Open(t)
	if err := Apply(conn); err != nil {
		t.Fatal(err)
	}
	s, _ := Load()
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM model_configs WHERE user_overrides = '[]'`).Scan(&n); err != nil || n != 30 {
		t.Fatalf("模型数=%d err=%v", n, err)
	}
	for _, m := range s.Models {
		r := modelByID(t, conn, m.ModelID)
		if r.ID != m.ID {
			t.Fatalf("%s 的 id=%d，必须保留生产 id %d", m.ModelID, r.ID, m.ID)
		}
	}
	for _, st := range s.Settings {
		if got := settingByKey(t, conn, st.Key); got.Value != st.Value || got.Overrides != nil {
			t.Fatalf("%s 与种子不一致", st.Key)
		}
	}
	h, ok, err := readSeedHash(context.Background(), conn, DatasetModelCenter)
	if err != nil || !ok || h != s.Hash {
		t.Fatalf("hash=%q ok=%v err=%v", h, ok, err)
	}
}

// 种子没变时第二次启动什么都不做（连库里的漂移也不纠正——说明根本没进合并）。
func TestApplySkipsWhenHashUnchanged(t *testing.T) {
	conn := dbtest.Open(t)
	s := mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", nil)}, nil)
	mustApply(t, conn, s)
	simulateUserEdit(t, conn, `UPDATE model_configs SET name = '漂移' WHERE model_id = 'img-a'`)
	mustApply(t, conn, s)
	if got := modelByID(t, conn, "img-a").Parts.Name; got != "漂移" {
		t.Fatalf("name=%q：哈希未变不应重新合并", got)
	}
}

func TestApplyUpgradeAddsAndRemovesModels(t *testing.T) {
	conn := dbtest.Open(t)
	mustApply(t, conn, mustSeed(t, []string{
		seedModelLine(t, 1, "img-a", "image", nil),
		seedModelLine(t, 2, "img-b", "image", nil),
	}, nil))
	mustApply(t, conn, mustSeed(t, []string{
		seedModelLine(t, 1, "img-a", "image", nil),
		seedModelLine(t, 3, "img-c", "image", nil),
	}, nil))
	rows, err := loadModelRows(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows["img-b"]; ok || len(rows) != 2 || rows["img-c"].ID != 3 {
		t.Fatalf("rows=%v", rows)
	}
}

func TestApplyUpgradeUntouchedFieldsFollowSeed(t *testing.T) {
	conn := dbtest.Open(t)
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"name": "A1"})}, nil))
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"name": "A2", "base_path": "/v2/images", "hidden": 1})}, nil))
	r := modelByID(t, conn, "img-a")
	if r.Parts.Name != "A2" || r.BasePath != "/v2/images" || !r.Parts.Hidden {
		t.Fatalf("got %+v", r)
	}
}

func TestApplyUpgradeKeepsUserChangedFields(t *testing.T) {
	conn := dbtest.Open(t)
	gc1 := map[string]any{"concurrency": 5, "modules": map[string]any{"canvas": map[string]any{"maxRetries": 0, "retryIntervalSec": 0}}}
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{
		"name": "A1", "ui_schema": ratioUI("1:1", "1:1", "16:9"), "gen_control": gc1,
	})}, nil))
	simulateUserEdit(t, conn, `UPDATE model_configs SET name = '我的', enabled = 0,
		ui_schema = '{"ratio":{"label":"比例","default":"16:9","options":[{"label":"1:1","value":"1:1"},{"label":"16:9","value":"16:9"}]}}',
		gen_control = '{"concurrency":9,"modules":{"canvas":{"maxRetries":0,"retryIntervalSec":0}}}',
		user_overrides = '["enabled","gen_control.concurrency","name","ui_schema.ratio.default"]' WHERE model_id = 'img-a'`)

	gc2 := map[string]any{"concurrency": 50, "modules": map[string]any{"canvas": map[string]any{"maxRetries": 2, "retryIntervalSec": 10}}, "inputLimits": map[string]any{"video": map[string]any{"max": 1}}}
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{
		"name": "A2", "hidden": 1, "ui_schema": ratioUI("1:1", "1:1", "16:9", "9:16"), "gen_control": gc2,
	})}, nil))

	r := modelByID(t, conn, "img-a")
	if r.Parts.Name != "我的" || r.Parts.Enabled || !r.Parts.Hidden {
		t.Fatalf("name/enabled 应保留用户值、hidden 应跟随新版：%+v", r.Parts)
	}
	doc, err := modelDoc(r.Parts)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"ui_schema.ratio.default":               `"16:9"`, // 用户值
		"gen_control.concurrency":               `9`,      // 用户值
		"gen_control.modules.canvas.maxRetries": `2`,      // 新版
		"gen_control.inputLimits.video.max":     `1`,      // 新版
	} {
		v, _ := getPath(doc, mustParsePath(path))
		if got, _ := encodeJSON(v); got != want {
			t.Errorf("%s = %s, want %s", path, got, want)
		}
	}
	if opts, _ := getPath(doc, mustParsePath("ui_schema.ratio.options")); len(opts.([]any)) != 3 {
		t.Errorf("新版新增的选项没有进来：%v", opts)
	}
	if !reflect.DeepEqual(r.Overrides, []string{"enabled", "gen_control.concurrency", "name", "ui_schema.ratio.default"}) {
		t.Fatalf("overrides=%v", r.Overrides)
	}
}

// Review Focus 1：用户选的默认值在新版里不再是可选项 → 丢弃该覆盖，用新版默认值。
func TestApplyDropsOverrideWhenDefaultNoLongerOffered(t *testing.T) {
	conn := dbtest.Open(t)
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"ui_schema": ratioUI("1:1", "1:1", "4:3")})}, nil))
	simulateUserEdit(t, conn, `UPDATE model_configs SET ui_schema = '{"ratio":{"label":"比例","default":"4:3","options":[{"label":"1:1","value":"1:1"},{"label":"4:3","value":"4:3"}]}}',
		user_overrides = '["ui_schema.ratio.default"]' WHERE model_id = 'img-a'`)
	s2 := mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"ui_schema": ratioUI("16:9", "1:1", "16:9")})}, nil)
	mustApply(t, conn, s2)
	r := modelByID(t, conn, "img-a")
	if r.Overrides != nil || *r.Parts.UISchema != *s2.Models[0].parts().UISchema {
		t.Fatalf("应回到新版 uiSchema 原文：overrides=%v ui=%s", r.Overrides, *r.Parts.UISchema)
	}
}

// 新版给同一个 model_id 换了 id（且旧 id 被另一个模型占用）：主键跟随种子，用户修改保留。
func TestApplyRenumberedModelKeepsOverride(t *testing.T) {
	conn := dbtest.Open(t)
	mustApply(t, conn, mustSeed(t, []string{
		seedModelLine(t, 1, "img-a", "image", nil),
		seedModelLine(t, 2, "img-b", "image", nil),
	}, nil))
	simulateUserEdit(t, conn, `UPDATE model_configs SET name = '我的 A', user_overrides = '["name"]' WHERE model_id = 'img-a'`)
	mustApply(t, conn, mustSeed(t, []string{
		seedModelLine(t, 2, "img-a", "image", nil),
		seedModelLine(t, 1, "img-b", "image", nil),
	}, nil))
	a, b := modelByID(t, conn, "img-a"), modelByID(t, conn, "img-b")
	if a.ID != 2 || b.ID != 1 || a.Parts.Name != "我的 A" {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
}

func lightSetting(t *testing.T, models string, template string, presets string) string {
	t.Helper()
	return seedSettingLine(t, SettingCanvasLightControl, `{"models":`+models+`,"template":"`+template+`","presets":`+presets+`}`)
}

func TestApplySettingOverrideKeptAndStructureUpdated(t *testing.T) {
	conn := dbtest.Open(t)
	img := []string{seedModelLine(t, 1, "img-a", "image", nil)}
	mustApply(t, conn, mustSeed(t, img, []string{
		lightSetting(t, `["img-a"]`, "旧 {light}", `[{"key":"p1","name":"P1","prompt":"内置 p1"}]`),
	}))
	simulateUserEdit(t, conn, `UPDATE settings SET value = ?, user_overrides = '["presets[p1].prompt"]' WHERE "key" = ?`,
		`{"models":["img-a"],"presets":[{"key":"p1","name":"P1","prompt":"我的 p1"}],"template":"旧 {light}"}`, SettingCanvasLightControl)
	mustApply(t, conn, mustSeed(t, img, []string{
		lightSetting(t, `["img-a"]`, "新 {light}", `[{"key":"p1","name":"P1 新名","prompt":"内置 p1 v2"},{"key":"p2","name":"P2","prompt":"p2"}]`),
	}))
	got := settingByKey(t, conn, SettingCanvasLightControl)
	// 键序跟新版种子原文（P2a M8）。
	want := `{"models":["img-a"],"template":"新 {light}","presets":[{"key":"p1","name":"P1 新名","prompt":"我的 p1"},{"key":"p2","name":"P2","prompt":"p2"}]}`
	if got.Value != want || !reflect.DeepEqual(got.Overrides, []string{"presets[p1].prompt"}) {
		t.Fatalf("got  %s %v\nwant %s", got.Value, got.Overrides, want)
	}
}

// Review Focus 2：用户选的候选模型在新版里被删了 → 丢弃该覆盖，用新版候选列表。
func TestApplyDropsSettingOverrideReferencingRemovedModel(t *testing.T) {
	conn := dbtest.Open(t)
	v1 := mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", nil), seedModelLine(t, 2, "img-b", "image", nil)},
		[]string{lightSetting(t, `["img-a"]`, "{light}", `[]`)})
	mustApply(t, conn, v1)
	simulateUserEdit(t, conn, `UPDATE settings SET value = '{"models":["img-b"],"presets":[],"template":"{light}"}', user_overrides = '["models"]' WHERE "key" = ?`, SettingCanvasLightControl)
	v2 := mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", nil)},
		[]string{lightSetting(t, `["img-a"]`, "{light}", `[]`)})
	mustApply(t, conn, v2)
	got := settingByKey(t, conn, SettingCanvasLightControl)
	if seedV, _ := v2.Setting(SettingCanvasLightControl); got.Value != seedV.Value || got.Overrides != nil {
		t.Fatalf("got %s %v", got.Value, got.Overrides)
	}
}

// Review Focus 4：合并中途失败 → 整体回滚，哈希不写入，下次启动重试。
func TestApplyRollsBackOnFailure(t *testing.T) {
	conn := dbtest.Open(t)
	v1 := mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", nil), seedModelLine(t, 2, "img-b", "image", nil)}, nil)
	mustApply(t, conn, v1)
	simulateUserEdit(t, conn, `CREATE TRIGGER boom BEFORE INSERT ON model_configs WHEN NEW.model_id = 'img-c' BEGIN SELECT RAISE(ABORT, 'boom'); END`)
	v2 := mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"name": "改名"}), seedModelLine(t, 3, "img-c", "image", nil)}, nil)
	if err := applySeed(context.Background(), conn, v2); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err=%v", err)
	}
	rows, _ := loadModelRows(context.Background(), conn)
	if _, ok := rows["img-b"]; !ok || rows["img-a"].Parts.Name != "img-a" {
		t.Fatalf("失败后库应保持 v1：%v", rows)
	}
	if h, _, _ := readSeedHash(context.Background(), conn, DatasetModelCenter); h != v1.Hash {
		t.Fatal("失败时不得写入新哈希")
	}
	simulateUserEdit(t, conn, `DROP TRIGGER boom`)
	mustApply(t, conn, v2)
	if _, err := loadModelRow(context.Background(), conn, "img-c"); err != nil {
		t.Fatalf("重试应成功：%v", err)
	}
}

func TestApplyLeavesNonSeedSettingsAndKeepsBaseDomainOverride(t *testing.T) {
	conn := dbtest.Open(t)
	models := []string{seedModelLine(t, 1, "img-a", "image", nil)}
	mustApply(t, conn, mustSeed(t, models, []string{seedSettingLine(t, SettingBaseDomain, "http://old.example:3000")}))
	simulateUserEdit(t, conn, `INSERT INTO settings ("key", value) VALUES (?, 'sk-user')`, SettingUpstreamAPIKey)
	simulateUserEdit(t, conn, `UPDATE settings SET value = 'https://mine.example', user_overrides = '["$"]' WHERE "key" = ?`, SettingBaseDomain)
	mustApply(t, conn, mustSeed(t, models, []string{seedSettingLine(t, SettingBaseDomain, "http://new.example:3000")}))
	if got := settingByKey(t, conn, SettingUpstreamAPIKey); got.Value != "sk-user" {
		t.Fatalf("upstream_api_key 被改动：%q", got.Value)
	}
	if got := settingByKey(t, conn, SettingBaseDomain); got.Value != "https://mine.example" {
		t.Fatalf("base_domain=%q，应保留用户值", got.Value)
	}
}
