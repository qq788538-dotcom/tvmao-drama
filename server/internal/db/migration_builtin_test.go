package db_test

import (
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// 已有行和新插入的行，user_overrides 都默认为 '[]'（全部字段跟随内置种子）。
func TestUserOverridesDefaultsToEmptyArray(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO settings ("key", value) VALUES ('k', 'v')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO model_configs (type, name, model_id, base_path, request_template) VALUES ('text', 'n', 'm', '/v1', '{}')`); err != nil {
		t.Fatal(err)
	}
	var s, m string
	if err := conn.QueryRow(`SELECT user_overrides FROM settings WHERE "key" = 'k'`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT user_overrides FROM model_configs WHERE model_id = 'm'`).Scan(&m); err != nil {
		t.Fatal(err)
	}
	if s != "[]" || m != "[]" {
		t.Fatalf("settings=%q model_configs=%q, want []", s, m)
	}
}

// user_overrides 是 JSON 列，只能以 TEXT 落库；传 []byte 会被存成 BLOB，必须被 CHECK 拒绝。
func TestUserOverridesRejectsBlob(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO settings ("key", value, user_overrides) VALUES ('k', 'v', ?)`, []byte(`[]`)); err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
		t.Fatalf("BLOB 应当被 CHECK 约束拒绝，得到 %v", err)
	}
}

func TestBuiltinSeedStateUpdatedAtTrigger(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO builtin_seed_state (dataset, hash, updated_at) VALUES ('d', 'h1', '2000-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE builtin_seed_state SET hash = 'h2' WHERE dataset = 'd'`); err != nil {
		t.Fatal(err)
	}
	var updated time.Time
	if err := conn.QueryRow(`SELECT updated_at FROM builtin_seed_state WHERE dataset = 'd'`).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Year() <= 2000 {
		t.Fatalf("updated_at 没有被触发器刷新：%v", updated)
	}
}
