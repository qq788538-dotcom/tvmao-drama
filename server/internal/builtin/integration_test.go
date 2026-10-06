package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// 跨 Apply 与 Service 的端到端约定（spec M10 + M11）：
// 经 Service 改过的字段在升级后保留；改回内置值或“恢复默认”后的字段在升级后跟随新版。
func TestServiceEditsSurviveUpgradeAndRestoreFollowsSeed(t *testing.T) {
	ctx := context.Background()
	conn := dbtest.Open(t)
	version := func(tag string) *Seed {
		return mustSeed(t, []string{
			seedModelLine(t, 1, "img-a", "image", map[string]any{"name": "A " + tag, "ui_schema": ratioUI("1:1", "1:1", "16:9")}),
			seedModelLine(t, 2, "img-b", "image", map[string]any{"name": "B " + tag}),
			seedModelLine(t, 3, "img-c", "image", map[string]any{"name": "C " + tag}),
		}, []string{
			seedSettingLine(t, SettingCanvasLightControl, `{"models":["img-a"],"template":"`+tag+` {light}","presets":[{"key":"p1","name":"P1","prompt":"内置 `+tag+`"}]}`),
		})
	}
	v1, v2 := version("v1"), version("v2")
	if err := applySeed(ctx, conn, v1); err != nil {
		t.Fatal(err)
	}
	svc := &Service{db: conn, seed: v1}
	if _, err := svc.UpdateModel(ctx, "img-a", edits("name", `"我的 A"`, "ui_schema.ratio.default", `"16:9"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateModel(ctx, "img-b", edits("name", `"临时 B"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RestoreModelDefault(ctx, "img-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateModel(ctx, "img-c", edits("name", `"临时 C"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateModel(ctx, "img-c", edits("name", `"C v1"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSetting(ctx, SettingCanvasLightControl, edits("presets[p1].prompt", `"我的提示词"`)); err != nil {
		t.Fatal(err)
	}

	if err := applySeed(ctx, conn, v2); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"img-a": "我的 A", "img-b": "B v2", "img-c": "C v2"} {
		if got := modelByID(t, conn, id).Parts.Name; got != want {
			t.Errorf("%s name=%q, want %q", id, got, want)
		}
	}
	if ui := *modelByID(t, conn, "img-a").Parts.UISchema; !strings.Contains(ui, `"default":"16:9"`) {
		t.Errorf("img-a 的默认比例应保留用户值：%s", ui)
	}
	light := settingByKey(t, conn, SettingCanvasLightControl).Value
	if !strings.Contains(light, `"prompt":"我的提示词"`) || !strings.Contains(light, `"template":"v2 {light}"`) {
		t.Errorf("light=%s", light)
	}
}

// 真实种子：Service 能在内置数据上完成一次典型编辑。
func TestNewServiceOnEmbeddedSeed(t *testing.T) {
	conn := dbtest.Open(t)
	if err := Apply(conn); err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := svc.UpdateSetting(ctx, SettingGenImagePresets, edits("presets[storyboard].value.prompt", `"我的故事板"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateModel(ctx, "gpt-image-2", edits("gen_control.modules.canvas.maxRetries", `1`)); err != nil {
		t.Fatal(err)
	}
}
