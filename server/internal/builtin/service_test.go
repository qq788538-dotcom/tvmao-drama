package builtin

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

// svcFixture 建库、直接写入小种子（不经 Apply），返回绑定该种子的 Service。
func svcFixture(t *testing.T, s *Seed) (*Service, *sql.DB) {
	t.Helper()
	conn := dbtest.Open(t)
	insertSeedRows(t, conn, s)
	return &Service{db: conn, seed: s}, conn
}

func svcSeedV1(t *testing.T) *Seed {
	return mustSeed(t, []string{
		seedModelLine(t, 1, "img-a", "image", map[string]any{"name": "A", "ui_schema": ratioUI("1:1", "1:1", "16:9")}),
		seedModelLine(t, 2, "img-b", "image", nil),
		seedModelLine(t, 3, "txt-a", "text", nil),
	}, []string{
		seedSettingLine(t, SettingBaseDomain, "http://gw.example:3000"),
		seedSettingLine(t, SettingCanvasLightControl, `{"models":["img-a"],"template":"{light}","presets":[{"key":"p1","name":"P1","direction":"","prompt":"内置"}]}`),
	})
}

func TestServiceUpdateModel(t *testing.T) {
	svc, _ := svcFixture(t, svcSeedV1(t))
	st, err := svc.UpdateModel(context.Background(), "img-a", edits(
		"name", `"我的 A"`, "enabled", `false`, "ui_schema.ratio.default", `"16:9"`, "gen_control.concurrency", `3`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "我的 A" || st.Enabled || st.GenConcurrency() != 3 || !strings.Contains(string(st.UISchema), `"default":"16:9"`) {
		t.Fatalf("state=%+v", st)
	}
	if !reflect.DeepEqual(st.Overrides, []string{"enabled", "gen_control.concurrency", "name", "ui_schema.ratio.default"}) {
		t.Fatalf("overrides=%v", st.Overrides)
	}
}

// Review Focus 5：一批编辑里夹带技术字段 → 整批拒绝，库不变。
func TestServiceUpdateModelRejectsTechnicalFieldAtomically(t *testing.T) {
	svc, conn := svcFixture(t, svcSeedV1(t))
	_, err := svc.UpdateModel(context.Background(), "img-a", edits("name", `"改了"`, "base_path", `"/evil"`))
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Path != "base_path" {
		t.Fatalf("err=%v", err)
	}
	r := modelByID(t, conn, "img-a")
	if r.Parts.Name != "A" || r.BasePath != "/v1/image" || r.Overrides != nil {
		t.Fatalf("库被改动：%+v", r)
	}
}

// Review Focus 3：把字段改回内置值 → 覆盖记录清除（升级时它因此跟随新版，见 integration_test.go）。
func TestServiceUpdateModelBackToSeedClearsOverride(t *testing.T) {
	svc, conn := svcFixture(t, svcSeedV1(t))
	ctx := context.Background()
	if _, err := svc.UpdateModel(ctx, "img-a", edits("name", `"临时"`)); err != nil {
		t.Fatal(err)
	}
	st, err := svc.UpdateModel(ctx, "img-a", edits("name", `"A"`))
	if err != nil || len(st.Overrides) != 0 {
		t.Fatalf("overrides=%v err=%v", st.Overrides, err)
	}
	if r := modelByID(t, conn, "img-a"); r.Overrides != nil || r.Parts.Name != "A" {
		t.Fatalf("row=%+v", r)
	}
}

func TestServiceRestoreModelDefault(t *testing.T) {
	s := svcSeedV1(t)
	svc, conn := svcFixture(t, s)
	ctx := context.Background()
	if _, err := svc.UpdateModel(ctx, "img-a", edits("name", `"我的"`, "hidden", `true`, "ui_schema.ratio.default", `"16:9"`)); err != nil {
		t.Fatal(err)
	}
	st, err := svc.RestoreModelDefault(ctx, "img-a")
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "A" || st.Hidden || len(st.Overrides) != 0 {
		t.Fatalf("state=%+v", st)
	}
	if got, want := *modelByID(t, conn, "img-a").Parts.UISchema, *s.Models[0].parts().UISchema; got != want {
		t.Fatalf("ui_schema=%s，应逐字节恢复为种子 %s", got, want)
	}
}

// 写死的配置：任何字段都写不进去，库不变。
func TestServiceUpdateSetting(t *testing.T) {
	svc, conn := svcFixture(t, svcSeedV1(t))
	ctx := context.Background()
	before := settingByKey(t, conn, SettingCanvasLightControl)
	for name, kv := range map[string][]string{
		"提示词":  {"presets[p1].prompt", `"我的提示词"`},
		"模型列表": {"models", `["img-b","img-a"]`},
		"结构字段": {"presets[p1].direction", `"top"`},
	} {
		var ve *ValidationError
		if _, err := svc.UpdateSetting(ctx, SettingCanvasLightControl, edits(kv...)); !errors.As(err, &ve) {
			t.Errorf("%s：err=%v，want ValidationError", name, err)
		}
	}
	if after := settingByKey(t, conn, SettingCanvasLightControl); after.Value != before.Value || after.Overrides != nil {
		t.Fatalf("库被改动：%+v", after)
	}
}

func TestServiceBaseDomain(t *testing.T) {
	svc, _ := svcFixture(t, svcSeedV1(t))
	ctx := context.Background()
	st, err := svc.UpdateSetting(ctx, SettingBaseDomain, edits("$", `"https://mine.example"`))
	if err != nil || st.Value != "https://mine.example" || !reflect.DeepEqual(st.Overrides, []string{"$"}) {
		t.Fatalf("st=%+v err=%v", st, err)
	}
	var ve *ValidationError
	if _, err := svc.UpdateSetting(ctx, SettingBaseDomain, edits("$", `"https://mine.example/"`)); !errors.As(err, &ve) {
		t.Fatalf("结尾 / 应当被拒绝：%v", err)
	}
	st, err = svc.RestoreSettingDefault(ctx, SettingBaseDomain)
	if err != nil || st.Value != "http://gw.example:3000" || len(st.Overrides) != 0 {
		t.Fatalf("st=%+v err=%v", st, err)
	}
}

func TestServiceNotFound(t *testing.T) {
	svc, conn := svcFixture(t, svcSeedV1(t))
	ctx := context.Background()
	if _, err := svc.UpdateModel(ctx, "nope", edits("name", `"x"`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	if _, err := svc.UpdateSetting(ctx, SettingUpstreamAPIKey, edits("$", `"sk"`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("upstream_api_key 不是内置配置：%v", err)
	}
	if _, err := svc.RestoreSettingDefault(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	if _, err := conn.Exec(`INSERT INTO settings ("key", value) VALUES (?, 'sk-user')`, SettingUpstreamAPIKey); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListSettings(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListSettings 只返回内置配置：%v %v", list, err)
	}
}

func TestServiceListModelsIncludesOverrides(t *testing.T) {
	svc, _ := svcFixture(t, svcSeedV1(t))
	ctx := context.Background()
	if _, err := svc.UpdateModel(ctx, "img-b", edits("sort_order", `5`)); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListModels(ctx)
	if err != nil || len(list) != 3 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	for _, m := range list {
		want := []string{}
		if m.ModelID == "img-b" {
			want = []string{"sort_order"}
		}
		if !reflect.DeepEqual(m.Overrides, want) {
			t.Fatalf("%s overrides=%v", m.ModelID, m.Overrides)
		}
	}
}
