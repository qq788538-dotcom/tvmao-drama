package builtin

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

func TestModelRowRoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := dbtest.Open(t)
	s := mustSeed(t, []string{
		seedModelLine(t, 7, "img-a", "image", map[string]any{"ui_schema": ratioUI("1:1", "1:1", "16:9"), "polling_template": map[string]any{"path": "/x"}}),
	}, nil)
	r := rowFromSeed(s.Models[0])
	r.Overrides = []string{"name"}
	if err := insertModelRow(ctx, conn, r); err != nil {
		t.Fatal(err)
	}
	got, err := loadModelRow(ctx, conn, "img-a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, r) {
		t.Fatalf("got  %+v\nwant %+v", got, r)
	}
	// JSON 列必须以 TEXT 落库（BLOB 会让 json 函数按 JSONB 解析）。
	var typ string
	if err := conn.QueryRow(`SELECT typeof(ui_schema) || typeof(request_template) || typeof(gen_control) FROM model_configs WHERE id = 7`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	if typ != "texttextnull" {
		t.Fatalf("typeof = %s", typ)
	}
	if _, err := loadModelRow(ctx, conn, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
}

func TestUpdateModelRowCanChangeID(t *testing.T) {
	ctx := context.Background()
	conn := dbtest.Open(t)
	s := mustSeed(t, []string{seedModelLine(t, 7, "img-a", "image", nil)}, nil)
	r := rowFromSeed(s.Models[0])
	if err := insertModelRow(ctx, conn, r); err != nil {
		t.Fatal(err)
	}
	r.ID = 70
	r.Parts.Name = "改过"
	if err := updateModelRow(ctx, conn, r); err != nil {
		t.Fatal(err)
	}
	if err := moveModelID(ctx, conn, 70, -70); err != nil {
		t.Fatal(err)
	}
	rows, err := loadModelRows(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if got := rows["img-a"]; got.ID != -70 || got.Parts.Name != "改过" {
		t.Fatalf("got %+v", got)
	}
	if err := deleteModelRow(ctx, conn, "img-a"); err != nil {
		t.Fatal(err)
	}
	if cat, _ := loadCatalog(ctx, conn); len(cat) != 0 {
		t.Fatalf("catalog=%v", cat)
	}
}

func TestSettingRowAndSeedHash(t *testing.T) {
	ctx := context.Background()
	conn := dbtest.Open(t)
	if _, found, err := loadSettingRow(ctx, conn, "k"); found || err != nil {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if err := upsertSettingRow(ctx, conn, settingRow{Key: "k", Value: `{"a":1}`, Overrides: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if err := upsertSettingRow(ctx, conn, settingRow{Key: "k", Value: `{"a":2}`}); err != nil {
		t.Fatal(err)
	}
	r, found, err := loadSettingRow(ctx, conn, "k")
	if err != nil || !found || r.Value != `{"a":2}` || r.Overrides != nil {
		t.Fatalf("r=%+v found=%v err=%v", r, found, err)
	}
	if _, ok, err := readSeedHash(ctx, conn, DatasetModelCenter); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	for _, h := range []string{"h1", "h2"} {
		if err := writeSeedHash(ctx, conn, DatasetModelCenter, h); err != nil {
			t.Fatal(err)
		}
	}
	if h, ok, err := readSeedHash(ctx, conn, DatasetModelCenter); !ok || err != nil || h != "h2" {
		t.Fatalf("h=%q ok=%v err=%v", h, ok, err)
	}
}

// settings.Repository.Set 写入的行没有覆盖记录，读出来是 nil。
func TestDecodeOverridesEmpty(t *testing.T) {
	if o, err := decodeOverrides("[]"); err != nil || o != nil {
		t.Fatalf("o=%v err=%v", o, err)
	}
	if _, err := decodeOverrides("{"); err == nil {
		t.Fatal("损坏的 user_overrides 应当报错")
	}
}
