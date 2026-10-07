package builtin

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// 2026-10-07 用户要求：五项画布配置写死为内置值，设置页不再提供编辑入口。
var lockedKeys = []string{SettingCanvasPostprocess, SettingGenImagePresets, SettingCanvasMultiAngle, SettingCanvasLightControl, SettingCanvasScript}

func TestLockedSettingsHaveNoEditableFields(t *testing.T) {
	for _, k := range lockedKeys {
		got, ok := SettingEditableFields(k)
		if !ok {
			t.Errorf("%s 应仍是内置配置", k)
		}
		if len(got) != 0 {
			t.Errorf("%s 的可改字段应为空，got %v", k, got)
		}
	}
	if got, _ := SettingEditableFields(SettingBaseDomain); !reflect.DeepEqual(got, []string{"$"}) {
		t.Fatalf("base_domain 的可改字段不应受影响：%v", got)
	}
}

// 真实种子：五项配置的任何字段都写不进去，模型编辑照常。
func TestLockedSettingsRejectEditsOnEmbeddedSeed(t *testing.T) {
	conn := dbtest.Open(t)
	if err := Apply(conn); err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tries := map[string][]string{
		SettingGenImagePresets:    {"presets[storyboard].value.prompt", `"我的故事板"`},
		SettingCanvasLightControl: {"models", `[]`},
		SettingCanvasMultiAngle:   {"template", `""`},
		SettingCanvasPostprocess:  {"upscaleModel", `""`},
		SettingCanvasScript:       {"concurrency", `3`},
	}
	for key, kv := range tries {
		before := settingByKey(t, conn, key)
		var ve *ValidationError
		if _, err := svc.UpdateSetting(ctx, key, edits(kv...)); !errors.As(err, &ve) {
			t.Errorf("%s：err=%v，want ValidationError", key, err)
		}
		if after := settingByKey(t, conn, key); after.Value != before.Value || after.Overrides != nil {
			t.Errorf("%s 被改动：%+v", key, after)
		}
	}
	if _, err := svc.UpdateModel(ctx, "gpt-image-2", edits("gen_control.modules.canvas.maxRetries", `1`)); err != nil {
		t.Fatalf("模型编辑不应受影响：%v", err)
	}
}

// 升级：五项配置的旧 user_overrides 一律作废、回到内置值；模型中心的修改和 base_domain 保留。
func TestApplyUpgradeDropsLockedSettingOverridesKeepsModelEdits(t *testing.T) {
	conn := dbtest.Open(t)
	models := []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"ui_schema": ratioUI("1:1", "1:1", "16:9")})}
	v1 := mustSeed(t, models, []string{
		seedSettingLine(t, SettingBaseDomain, "http://gw.example:3000"),
		lightSetting(t, `["img-a"]`, "旧 {light}", `[{"key":"p1","name":"P1","prompt":"内置"}]`),
	})
	mustApply(t, conn, v1)
	simulateUserEdit(t, conn, `UPDATE settings SET value = ?, user_overrides = '["presets[p1].prompt"]' WHERE "key" = ?`,
		`{"models":["img-a"],"template":"旧 {light}","presets":[{"key":"p1","name":"P1","prompt":"我的"}]}`, SettingCanvasLightControl)
	simulateUserEdit(t, conn, `UPDATE settings SET value = 'https://mine.example', user_overrides = '["$"]' WHERE "key" = ?`, SettingBaseDomain)
	simulateUserEdit(t, conn, `UPDATE model_configs SET name = '我的 A', user_overrides = '["name"]' WHERE model_id = 'img-a'`)

	v2 := mustSeed(t, models, []string{
		seedSettingLine(t, SettingBaseDomain, "http://gw2.example:3000"),
		lightSetting(t, `["img-a"]`, "新 {light}", `[{"key":"p1","name":"P1","prompt":"内置 v2"}]`),
	})
	mustApply(t, conn, v2)
	seedLight, _ := v2.Setting(SettingCanvasLightControl)
	if got := settingByKey(t, conn, SettingCanvasLightControl); got.Value != seedLight.Value || got.Overrides != nil {
		t.Fatalf("打光应回到内置值：%s %v", got.Value, got.Overrides)
	}
	if got := settingByKey(t, conn, SettingBaseDomain); got.Value != "https://mine.example" {
		t.Fatalf("base_domain=%q，应保留用户值", got.Value)
	}
	if r := modelByID(t, conn, "img-a"); r.Parts.Name != "我的 A" || !reflect.DeepEqual(r.Overrides, []string{"name"}) {
		t.Fatalf("模型修改应保留：%+v", r)
	}
}

// 种子没变的启动：五项配置残留的覆盖也要清掉（库是写死之前的版本留下的），模型与 base_domain 不动。
func TestApplyResetsLockedSettingsEvenWhenHashUnchanged(t *testing.T) {
	conn := dbtest.Open(t)
	s := mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", nil)}, []string{
		seedSettingLine(t, SettingBaseDomain, "http://gw.example:3000"),
		lightSetting(t, `["img-a"]`, "{light}", `[]`),
	})
	mustApply(t, conn, s)
	simulateUserEdit(t, conn, `UPDATE settings SET value = '{"models":[],"template":"{light}","presets":[]}', user_overrides = '["models"]' WHERE "key" = ?`, SettingCanvasLightControl)
	simulateUserEdit(t, conn, `UPDATE settings SET value = 'https://mine.example', user_overrides = '["$"]' WHERE "key" = ?`, SettingBaseDomain)
	simulateUserEdit(t, conn, `UPDATE model_configs SET name = '我的', user_overrides = '["name"]' WHERE model_id = 'img-a'`)
	mustApply(t, conn, s)
	seedLight, _ := s.Setting(SettingCanvasLightControl)
	if got := settingByKey(t, conn, SettingCanvasLightControl); got.Value != seedLight.Value || got.Overrides != nil {
		t.Fatalf("打光应回到内置值：%s %v", got.Value, got.Overrides)
	}
	if got := settingByKey(t, conn, SettingBaseDomain); got.Value != "https://mine.example" {
		t.Fatalf("base_domain=%q", got.Value)
	}
	if r := modelByID(t, conn, "img-a"); r.Parts.Name != "我的" {
		t.Fatalf("模型修改应保留：%+v", r)
	}
}
