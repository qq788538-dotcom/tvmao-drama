// server/internal/models/repository_test.go
package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return dbtest.Open(t)
}

// TestGetByID 同时覆盖正常路径（新建的行通过主键往返查回）
// 和未找到路径（ErrNotFound）。
func TestGetByID(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	repo := NewSQLiteRepository(conn)

	const slug = "test-getbyid-slug"

	created, err := repo.Create(ModelConfig{
		Type:            "image",
		Name:            "test-getbyid-model",
		ModelID:         slug,
		BasePath:        "/v1/test",
		Enabled:         true,
		RequestTemplate: json.RawMessage(`{"prompt":"{{.prompt}}"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(created.ID) })

	got, err := repo.GetByID(created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID || got.ModelID != slug {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	if _, err := repo.GetByID(987654321); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestImageProtocolFieldsRoundTrip(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	repo := NewSQLiteRepository(conn)

	const slug = "test-image-protocol-slug"

	created, err := repo.Create(ModelConfig{
		Type:             "image",
		Name:             "test-image-protocol-model",
		ModelID:          slug,
		BasePath:         "/v1/images/generations",
		Enabled:          true,
		RequestTemplate:  json.RawMessage(`{"model":"x"}`),
		ImageInputFormat: ImageInputFormatDataURI,
		ImageRequestMode: ImageRequestModeOpenAIImageEdits,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(created.ID) })

	got, err := repo.GetByModelID(slug)
	if err != nil {
		t.Fatalf("GetByModelID: %v", err)
	}
	if got.ImageInputFormat != ImageInputFormatDataURI {
		t.Fatalf("ImageInputFormat = %q, want %q", got.ImageInputFormat, ImageInputFormatDataURI)
	}
	if got.ImageRequestMode != ImageRequestModeOpenAIImageEdits {
		t.Fatalf("ImageRequestMode = %q, want %q", got.ImageRequestMode, ImageRequestModeOpenAIImageEdits)
	}

	updated, err := repo.Update(created.ID, ModelConfig{
		Type:            "image",
		Name:            "test-image-protocol-model",
		ModelID:         slug,
		BasePath:        "/v1/images/generations",
		Enabled:         true,
		RequestTemplate: json.RawMessage(`{"model":"x"}`),
	})
	if err != nil {
		t.Fatalf("Update defaults: %v", err)
	}
	if updated.ImageInputFormat != ImageInputFormatURL || updated.ImageRequestMode != ImageRequestModeJSON {
		t.Fatalf("updated defaults = %q/%q", updated.ImageInputFormat, updated.ImageRequestMode)
	}
}

func TestAdminNoteRoundTrip(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	repo := NewSQLiteRepository(conn)

	const slug = "test-admin-note-slug"

	created, err := repo.Create(ModelConfig{
		Type:            "image",
		Name:            "test-admin-note-model",
		ModelID:         slug,
		BasePath:        "/v1/images/generations",
		AdminNote:       "用于 Gemini inlineData，图片输入选择 base64",
		Enabled:         true,
		RequestTemplate: json.RawMessage(`{"model":"x"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(created.ID) })

	got, err := repo.GetByModelID(slug)
	if err != nil {
		t.Fatalf("GetByModelID: %v", err)
	}
	if got.AdminNote != "用于 Gemini inlineData，图片输入选择 base64" {
		t.Fatalf("AdminNote = %q", got.AdminNote)
	}

	updated, err := repo.Update(created.ID, ModelConfig{
		Type:            "image",
		Name:            "test-admin-note-model",
		ModelID:         slug,
		BasePath:        "/v1/images/generations",
		AdminNote:       "更新后的后台备注",
		Enabled:         true,
		RequestTemplate: json.RawMessage(`{"model":"x"}`),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.AdminNote != "更新后的后台备注" {
		t.Fatalf("updated AdminNote = %q", updated.AdminNote)
	}
}

// JSON 列必须以 TEXT 落库：BLOB 会被 SQLite 的 json 函数当作 JSONB。
func TestJSONColumnsStoredAsText(t *testing.T) {
	conn := openTestDB(t)
	repo := NewSQLiteRepository(conn)
	m, err := repo.Create(ModelConfig{
		Type: "video", Name: "n", ModelID: "json-text-check", BasePath: "/v1/x",
		Enabled:          true,
		RequestTemplate:  json.RawMessage(`{"model":"x"}`),
		PollingTemplate:  json.RawMessage(`{"path":"/t/{{task_id}}"}`),
		UISchema:         json.RawMessage(`{"duration":{"default":5}}`),
		BillingConfigRaw: json.RawMessage(`{"flatFee":1}`),
		GenControlRaw:    json.RawMessage(`{"concurrency":3}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"request_template", "polling_template", "ui_schema", "billing_config", "gen_control"} {
		var typ string
		if err := conn.QueryRow(`SELECT typeof(`+col+`) FROM model_configs WHERE id = ?`, m.ID).Scan(&typ); err != nil {
			t.Fatal(err)
		}
		if typ != "text" {
			t.Fatalf("%s 存成了 %s，want text", col, typ)
		}
	}
	got, err := repo.GetByID(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.GenConcurrency() != 3 || string(got.UISchema) != `{"duration":{"default":5}}` {
		t.Fatalf("回读不一致: concurrency=%d uiSchema=%s", got.GenConcurrency(), got.UISchema)
	}

	// Update 也必须以 TEXT 落库。
	m.UISchema = json.RawMessage(`{"duration":{"default":10}}`)
	m.GenControlRaw = json.RawMessage(`{"concurrency":5}`)
	if _, err := repo.Update(m.ID, m); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"request_template", "polling_template", "ui_schema", "billing_config", "gen_control"} {
		var typ string
		if err := conn.QueryRow(`SELECT typeof(`+col+`) FROM model_configs WHERE id = ?`, m.ID).Scan(&typ); err != nil {
			t.Fatal(err)
		}
		if typ != "text" {
			t.Fatalf("Update 后 %s 存成了 %s，want text", col, typ)
		}
	}
}

func TestListOnlyVisibleFiltersDisabledAndHidden(t *testing.T) {
	repo := NewSQLiteRepository(openTestDB(t))
	mk := func(id string, enabled, hidden bool, sort int) {
		if _, err := repo.Create(ModelConfig{Type: "image", Name: id, ModelID: id, BasePath: "/p",
			Enabled: enabled, Hidden: hidden, SortOrder: sort}); err != nil {
			t.Fatal(err)
		}
	}
	mk("visible-b", true, false, 2)
	mk("visible-a", true, false, 1)
	mk("disabled", false, false, 0)
	mk("hidden", true, true, 0)
	list, err := repo.List(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ModelID != "visible-a" || list[1].ModelID != "visible-b" {
		t.Fatalf("list=%v", list)
	}
	all, _ := repo.List(false)
	if len(all) != 4 {
		t.Fatalf("all=%d want 4", len(all))
	}
}

func TestCreateDuplicateModelIDFails(t *testing.T) {
	repo := NewSQLiteRepository(openTestDB(t))
	m := ModelConfig{Type: "image", Name: "n", ModelID: "dup", BasePath: "/p"}
	if _, err := repo.Create(m); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(m); err == nil {
		t.Fatal("重复的 model_id 应当违反唯一约束")
	}
}

// 可选 JSON 列的字面量 null / 空值写成 SQL NULL；request_template 是 NOT NULL 列，
// 字面量 null 原样存成文本 "null"（钉住现状）。
func TestNullJSONLiteralsBecomeSQLNull(t *testing.T) {
	conn := openTestDB(t)
	repo := NewSQLiteRepository(conn)
	m, err := repo.Create(ModelConfig{
		Type: "image", Name: "n", ModelID: "null-json", BasePath: "/v1/x",
		RequestTemplate:  json.RawMessage(`null`),
		PollingTemplate:  json.RawMessage(`null`),
		UISchema:         json.RawMessage(``),
		BillingConfigRaw: json.RawMessage(`null`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"polling_template", "ui_schema", "billing_config", "gen_control"} {
		var isNull bool
		if err := conn.QueryRow(`SELECT `+col+` IS NULL FROM model_configs WHERE id = ?`, m.ID).Scan(&isNull); err != nil {
			t.Fatal(err)
		}
		if !isNull {
			t.Fatalf("%s 应为 SQL NULL", col)
		}
	}
	var rt string
	if err := conn.QueryRow(`SELECT request_template FROM model_configs WHERE id = ?`, m.ID).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	if rt != "null" {
		t.Fatalf("request_template = %q, want \"null\"", rt)
	}
}
