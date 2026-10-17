package generation

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/james/huabu_studio/apps/api/internal/assets"
)

// 只给 *_db_test.go 用的 SQLite 测试替身。生产实现分别在 assets / library 包和
// cmd/sidecar/gen_wiring.go；这里用同形 SQL 直写，让 generation 的真实库测试不依赖它们。

// billingCall / billingSpy：桌面端计费是空实现，「钱动没动」的断言落在
// 「收口事务里调没调、调了几次 Settle/Refund」上——结构性保证不变。
type billingCall struct {
	Op     string
	TaskID int64
	Frozen int64
	Actual int64
}

type billingSpy struct {
	mu        sync.Mutex
	calls     []billingCall
	freezeErr error
}

func (b *billingSpy) record(c billingCall) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, c)
}

func (b *billingSpy) ops() []billingCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]billingCall(nil), b.calls...)
}

func (b *billingSpy) Freeze(_ context.Context, _, t, a int64, _ string) (BillingResult, error) {
	b.record(billingCall{Op: "Freeze", TaskID: t, Frozen: a})
	return BillingResult{}, nil
}

func (b *billingSpy) Settle(_ context.Context, _, t, f, a int64, _ string) (BillingResult, error) {
	b.record(billingCall{Op: "Settle", TaskID: t, Frozen: f, Actual: a})
	return BillingResult{}, nil
}

func (b *billingSpy) Refund(_ context.Context, _, t, f int64, _ string) (BillingResult, error) {
	b.record(billingCall{Op: "Refund", TaskID: t, Frozen: f})
	return BillingResult{}, nil
}

func (b *billingSpy) FreezeTx(_ context.Context, _ *sql.Tx, _, t, a int64, _ string) (BillingResult, error) {
	b.record(billingCall{Op: "FreezeTx", TaskID: t, Frozen: a})
	return BillingResult{}, b.freezeErr
}

func (b *billingSpy) SettleTx(_ context.Context, _ *sql.Tx, _, t, f, a int64, _ string) (BillingResult, error) {
	b.record(billingCall{Op: "SettleTx", TaskID: t, Frozen: f, Actual: a})
	return BillingResult{}, nil
}

func (b *billingSpy) RefundTx(_ context.Context, _ *sql.Tx, _, t, f int64, _ string) (BillingResult, error) {
	b.record(billingCall{Op: "RefundTx", TaskID: t, Frozen: f})
	return BillingResult{}, nil
}

func spyOf(s *Service) *billingSpy { return s.billing.(*billingSpy) }

// sqlTestAssets 实现 AssetRecorder（槽位幂等语义与 assets 包的实现一致）。
type sqlTestAssets struct{ db *sql.DB }

func (a sqlTestAssets) Insert(rec assets.AssetRecord) (int64, error) {
	res, err := a.db.Exec(`INSERT INTO assets (user_id, project_id, source_task_id, object_key, source_kind, url, size_bytes, mime_type)
		VALUES (?, NULLIF(?, 0), NULLIF(?, 0), ?, ?, ?, ?, ?)`,
		rec.UserID, rec.ProjectID, rec.SourceTaskID, rec.ObjectKey, rec.SourceKind, rec.URL, rec.SizeBytes, rec.MimeType)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (a sqlTestAssets) FindByTaskSlot(taskID int64, outputIndex int) (*assets.AssetRecord, error) {
	var r assets.AssetRecord
	err := a.db.QueryRow(`SELECT id, user_id, object_key, url, size_bytes, mime_type FROM assets
		WHERE source_task_id = ? AND output_index = ?`, taskID, outputIndex).
		Scan(&r.ID, &r.UserID, &r.ObjectKey, &r.URL, &r.SizeBytes, &r.MimeType)
	if err != nil {
		return nil, err
	}
	r.SourceTaskID = taskID
	return &r, nil
}

func (a sqlTestAssets) InsertSlotTx(tx *sql.Tx, rec assets.AssetRecord, outputIndex int) (int64, bool, error) {
	var id int64
	err := tx.QueryRow(`INSERT INTO assets (user_id, project_id, source_task_id, object_key, source_kind, url, size_bytes, mime_type, duration_ms, output_index)
		VALUES (?, NULLIF(?, 0), ?, ?, ?, ?, ?, ?, NULLIF(?, 0), ?)
		ON CONFLICT (source_task_id, output_index) DO NOTHING
		RETURNING id`,
		rec.UserID, rec.ProjectID, rec.SourceTaskID, rec.ObjectKey, rec.SourceKind, rec.URL, rec.SizeBytes, rec.MimeType, rec.DurationMs, outputIndex).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	err = tx.QueryRow(`SELECT id FROM assets WHERE source_task_id = ? AND output_index = ?`, rec.SourceTaskID, outputIndex).Scan(&id)
	return id, false, err
}

func (a sqlTestAssets) ListForTask(taskID int64) ([]assets.AssetRecord, error) {
	rows, err := a.db.Query(`SELECT id, user_id, object_key, url, size_bytes, mime_type FROM assets WHERE source_task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []assets.AssetRecord
	for rows.Next() {
		var r assets.AssetRecord
		if err := rows.Scan(&r.ID, &r.UserID, &r.ObjectKey, &r.URL, &r.SizeBytes, &r.MimeType); err != nil {
			return nil, err
		}
		r.SourceTaskID = taskID
		out = append(out, r)
	}
	return out, rows.Err()
}

func (a sqlTestAssets) DeleteForTask(taskID int64) (int64, error) {
	res, err := a.db.Exec(`DELETE FROM assets WHERE source_task_id = ?`, taskID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (a sqlTestAssets) GetDurationMsByObjectKey(key string) (int64, bool, error) {
	var ms sql.NullInt64
	err := a.db.QueryRow(`SELECT duration_ms FROM assets WHERE object_key = ? ORDER BY id DESC LIMIT 1`, key).Scan(&ms)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return ms.Int64, true, nil
}

func (a sqlTestAssets) SetDurationMsByObjectKey(key string, ms int64) error {
	_, err := a.db.Exec(`UPDATE assets SET duration_ms = ? WHERE object_key = ?`, ms, key)
	return err
}

type sqlTestUsers struct{ db *sql.DB }

func (u sqlTestUsers) AddStorage(id, delta int64) error {
	_, err := u.db.Exec(`UPDATE users SET storage_used_bytes = storage_used_bytes + ? WHERE id = ?`, delta, id)
	return err
}

func (u sqlTestUsers) StorageUsage(id int64) (int64, int64, error) {
	var used, quota int64
	err := u.db.QueryRow(`SELECT storage_used_bytes, storage_quota_bytes FROM users WHERE id = ?`, id).Scan(&used, &quota)
	return used, quota, err
}

func (u sqlTestUsers) AddStorageTx(tx *sql.Tx, id, delta int64) error {
	_, err := tx.Exec(`UPDATE users SET storage_used_bytes = storage_used_bytes + ? WHERE id = ?`, delta, id)
	return err
}

// sqlTestReclaimer 只看 assets 引用（library_items / orphan_objects 属于 library 包），
// 被判定无引用的 key 记进 marked。
type sqlTestReclaimer struct{ marked *[]string }

func (r sqlTestReclaimer) ReclaimTx(tx *sql.Tx, key string, _, _ int64) (bool, error) {
	var referenced bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM assets WHERE object_key = ?)`, key).Scan(&referenced); err != nil {
		return false, err
	}
	if referenced {
		return false, nil
	}
	*r.marked = append(*r.marked, key)
	return true, nil
}
