package builtin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
)

// 本文件是本包唯一直接写 SQL 的地方。所有函数都接收 querier（*sql.DB 或 *sql.Tx）：
// 在事务里必须传 tx——持有事务时再经 *sql.DB 写库会等满 busy_timeout 后 SQLITE_BUSY（p1-research.md 第 6 节）。
// 不复用 models.SQLiteRepository 的写方法，原因同上（它固定用 r.db）。

// ErrNotFound 表示要改的模型或配置不是内置数据。
var ErrNotFound = errors.New("内置数据不存在")

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// modelRow 是 model_configs 的一行（不含时间戳）。
type modelRow struct {
	ID               int64
	ModelID          string
	Type             string
	BasePath         string
	Parts            modelParts
	RequestTemplate  string
	PollingTemplate  *string
	BillingConfig    *string
	ImageInputFormat string
	ImageRequestMode string
	AdminNote        string
	PriceNote        string
	Overrides        []string
}

func rowFromSeed(m SeedModel) modelRow {
	r := modelRow{
		ID:               m.ID,
		ModelID:          m.ModelID,
		Type:             m.Type,
		BasePath:         m.BasePath,
		Parts:            m.parts(),
		RequestTemplate:  string(nullableRaw(m.RequestTemplate)),
		PollingTemplate:  rawPtr(m.PollingTemplate),
		BillingConfig:    rawPtr(m.BillingConfig),
		ImageInputFormat: m.ImageInputFormat,
		ImageRequestMode: m.ImageRequestMode,
		AdminNote:        m.AdminNote,
		PriceNote:        m.PriceNote,
	}
	// 与 models 仓库的 normalizeImageProtocolDefaults 一致。
	if r.ImageInputFormat == "" {
		r.ImageInputFormat = "url"
	}
	if r.ImageRequestMode == "" {
		r.ImageRequestMode = "json"
	}
	return r
}

func encodeOverrides(o []string) string {
	if len(o) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(o)
	return string(b)
}

func decodeOverrides(s string) ([]string, error) {
	var o []string
	if err := json.Unmarshal([]byte(s), &o); err != nil {
		return nil, fmt.Errorf("user_overrides 不是字符串数组: %w", err)
	}
	if len(o) == 0 {
		return nil, nil
	}
	return o, nil
}

// decodeOverridesLenient 读库里的覆盖记录：坏数据（手改库、诊断工具写错）只记一条警告并当作空，
// 该行回到“跟随内置”，不能因为它让 sidecar 起不来。
func decodeOverridesLenient(s, what string) []string {
	o, err := decodeOverrides(s)
	if err != nil {
		log.Printf("内置数据：%s 的 user_overrides 无法解析（%v），当作没有用户修改", what, err)
		return nil
	}
	return o
}

// nullStr 把 *string 转成 SQL 参数：nil → NULL，否则 string（TEXT 落库，不能传 []byte）。
func nullStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func strPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.String
	return &s
}

const modelRowColumns = `id, model_id, type, base_path, name, enabled, hidden, sort_order, ui_schema, gen_control,
	request_template, polling_template, billing_config, image_input_format, image_request_mode, admin_note, price_note, user_overrides`

func scanModelRow(sc interface{ Scan(...any) error }) (modelRow, error) {
	var r modelRow
	var ui, gc, poll, bill sql.NullString
	var ov string
	if err := sc.Scan(&r.ID, &r.ModelID, &r.Type, &r.BasePath, &r.Parts.Name, &r.Parts.Enabled, &r.Parts.Hidden, &r.Parts.SortOrder,
		&ui, &gc, &r.RequestTemplate, &poll, &bill, &r.ImageInputFormat, &r.ImageRequestMode, &r.AdminNote, &r.PriceNote, &ov); err != nil {
		return modelRow{}, err
	}
	r.Parts.UISchema, r.Parts.GenControl = strPtr(ui), strPtr(gc)
	r.PollingTemplate, r.BillingConfig = strPtr(poll), strPtr(bill)
	r.Overrides = decodeOverridesLenient(ov, "模型 "+r.ModelID)
	return r, nil
}

// loadModelRows 读出全部模型行，按 model_id 索引。
func loadModelRows(ctx context.Context, q querier) (map[string]modelRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+modelRowColumns+` FROM model_configs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]modelRow{}
	for rows.Next() {
		r, err := scanModelRow(rows)
		if err != nil {
			return nil, err
		}
		out[r.ModelID] = r
	}
	return out, rows.Err()
}

func loadModelRow(ctx context.Context, q querier, modelID string) (modelRow, error) {
	r, err := scanModelRow(q.QueryRowContext(ctx, `SELECT `+modelRowColumns+` FROM model_configs WHERE model_id = ?`, modelID))
	if errors.Is(err, sql.ErrNoRows) {
		return modelRow{}, ErrNotFound
	}
	return r, err
}

func insertModelRow(ctx context.Context, q querier, r modelRow) error {
	_, err := q.ExecContext(ctx, `INSERT INTO model_configs (id, model_id, type, base_path, name, enabled, hidden, sort_order, ui_schema, gen_control,
		request_template, polling_template, billing_config, image_input_format, image_request_mode, admin_note, price_note, user_overrides)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ModelID, r.Type, r.BasePath, r.Parts.Name, r.Parts.Enabled, r.Parts.Hidden, r.Parts.SortOrder,
		nullStr(r.Parts.UISchema), nullStr(r.Parts.GenControl), r.RequestTemplate, nullStr(r.PollingTemplate), nullStr(r.BillingConfig),
		r.ImageInputFormat, r.ImageRequestMode, r.AdminNote, r.PriceNote, encodeOverrides(r.Overrides))
	return err
}

// updateModelRow 按 model_id 整行覆盖，主键 id 也一并写成 r.ID（升级时种子可能给同一模型换了 id）。
func updateModelRow(ctx context.Context, q querier, r modelRow) error {
	_, err := q.ExecContext(ctx, `UPDATE model_configs SET id = ?, type = ?, base_path = ?, name = ?, enabled = ?, hidden = ?, sort_order = ?,
		ui_schema = ?, gen_control = ?, request_template = ?, polling_template = ?, billing_config = ?, image_input_format = ?,
		image_request_mode = ?, admin_note = ?, price_note = ?, user_overrides = ? WHERE model_id = ?`,
		r.ID, r.Type, r.BasePath, r.Parts.Name, r.Parts.Enabled, r.Parts.Hidden, r.Parts.SortOrder,
		nullStr(r.Parts.UISchema), nullStr(r.Parts.GenControl), r.RequestTemplate, nullStr(r.PollingTemplate), nullStr(r.BillingConfig),
		r.ImageInputFormat, r.ImageRequestMode, r.AdminNote, r.PriceNote, encodeOverrides(r.Overrides), r.ModelID)
	return err
}

func deleteModelRow(ctx context.Context, q querier, modelID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM model_configs WHERE model_id = ?`, modelID)
	return err
}

func moveModelID(ctx context.Context, q querier, from, to int64) error {
	_, err := q.ExecContext(ctx, `UPDATE model_configs SET id = ? WHERE id = ?`, to, from)
	return err
}

// loadCatalog 读出 model_id → type，供配置里的模型引用校验。
func loadCatalog(ctx context.Context, q querier) (Catalog, error) {
	rows, err := q.QueryContext(ctx, `SELECT model_id, type FROM model_configs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cat := Catalog{}
	for rows.Next() {
		var id, typ string
		if err := rows.Scan(&id, &typ); err != nil {
			return nil, err
		}
		cat[id] = typ
	}
	return cat, rows.Err()
}

type settingRow struct {
	Key       string
	Value     string
	Overrides []string
}

func loadSettingRow(ctx context.Context, q querier, key string) (settingRow, bool, error) {
	r := settingRow{Key: key}
	var ov string
	err := q.QueryRowContext(ctx, `SELECT value, user_overrides FROM settings WHERE "key" = ?`, key).Scan(&r.Value, &ov)
	if errors.Is(err, sql.ErrNoRows) {
		return settingRow{}, false, nil
	}
	if err != nil {
		return settingRow{}, false, err
	}
	r.Overrides = decodeOverridesLenient(ov, "配置 "+key)
	return r, true, nil
}

func upsertSettingRow(ctx context.Context, q querier, r settingRow) error {
	_, err := q.ExecContext(ctx, `INSERT INTO settings ("key", value, user_overrides) VALUES (?, ?, ?)
		ON CONFLICT ("key") DO UPDATE SET value = excluded.value, user_overrides = excluded.user_overrides`,
		r.Key, r.Value, encodeOverrides(r.Overrides))
	return err
}

func readSeedHash(ctx context.Context, q querier, dataset string) (string, bool, error) {
	var h string
	err := q.QueryRowContext(ctx, `SELECT hash FROM builtin_seed_state WHERE dataset = ?`, dataset).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return h, err == nil, err
}

func writeSeedHash(ctx context.Context, q querier, dataset, hash string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO builtin_seed_state (dataset, hash) VALUES (?, ?)
		ON CONFLICT (dataset) DO UPDATE SET hash = excluded.hash, applied_at = CURRENT_TIMESTAMP`, dataset, hash)
	return err
}
