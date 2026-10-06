package builtin

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// 测试用的小种子。字段与 seed/model_configs.jsonl 一致，over 覆盖默认值。

func seedModelLine(t *testing.T, id int64, modelID, typ string, over map[string]any) string {
	t.Helper()
	m := map[string]any{
		"id": id, "type": typ, "name": modelID, "model_id": modelID, "base_path": "/v1/" + typ,
		"enabled": 1, "hidden": 0, "sort_order": 0,
		"request_template": map[string]any{"model": modelID}, "polling_template": nil, "ui_schema": nil,
		"billing_config": map[string]any{"items": nil, "flatFee": 0, "freezeEstimate": 0}, "gen_control": nil,
		"image_input_format": "url", "image_request_mode": "json", "admin_note": "", "price_note": "",
		"created_at": "2026-01-01 00:00:00.000000", "updated_at": "2026-01-01 00:00:00.000000",
	}
	for k, v := range over {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func seedSettingLine(t *testing.T, key, value string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"key": key, "value": value, "updated_at": "2026-01-01 00:00:00.000000"})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustSeed(t *testing.T, models, settings []string) *Seed {
	t.Helper()
	s, err := parseSeed([]byte(strings.Join(models, "\n")+"\n"), []byte(strings.Join(settings, "\n")+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// ratioUI 是带两个选项的 uiSchema。
func ratioUI(def string, values ...string) map[string]any {
	opts := []any{}
	for _, v := range values {
		opts = append(opts, map[string]any{"label": v, "value": v})
	}
	return map[string]any{"ratio": map[string]any{"label": "比例", "default": def, "options": opts}}
}

// insertSeedRows 把小种子直接写进库（不经 Apply），给不依赖合并逻辑的测试用。
func insertSeedRows(t *testing.T, conn *sql.DB, s *Seed) {
	t.Helper()
	ctx := context.Background()
	for _, m := range s.Models {
		if err := insertModelRow(ctx, conn, rowFromSeed(m)); err != nil {
			t.Fatal(err)
		}
	}
	for _, st := range s.Settings {
		if err := upsertSettingRow(ctx, conn, settingRow{Key: st.Key, Value: st.Value}); err != nil {
			t.Fatal(err)
		}
	}
}

func modelByID(t *testing.T, conn *sql.DB, modelID string) modelRow {
	t.Helper()
	r, err := loadModelRow(context.Background(), conn, modelID)
	if err != nil {
		t.Fatalf("%s: %v", modelID, err)
	}
	return r
}

func settingByKey(t *testing.T, conn *sql.DB, key string) settingRow {
	t.Helper()
	r, found, err := loadSettingRow(context.Background(), conn, key)
	if err != nil || !found {
		t.Fatalf("%s: found=%v err=%v", key, found, err)
	}
	return r
}
