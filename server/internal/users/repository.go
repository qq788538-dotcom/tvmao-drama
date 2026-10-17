// Package users 是桌面端的本地用户仓库：只有 id=1 一个用户（迁移 0001 预置），
// 只保留画布链路用到的读取与存储用量方法；没有注册、团队影子用户、会员配额、改密等方法。
package users

import (
	"database/sql"
	"errors"
	"time"
)

type User struct {
	ID                int64
	Email             string
	Status            string
	Role              string
	StorageQuotaBytes int64
	StorageUsedBytes  int64
	BalanceCredits    int64
	FrozenCredits     int64
	// UpstreamAPIKey 非空 = 该用户上游调用用自己的 SK；空 = 回落全局 settings.upstream_api_key（生成链路用）。
	UpstreamAPIKey string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

var ErrNotFound = errors.New("user not found")

type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

// GetByID 读一个用户。桌面端 users 表没有 kind、password_changed_at 列，这里只选存在的列。
func (r *SQLiteRepository) GetByID(id int64) (*User, error) {
	var u User
	err := r.db.QueryRow(`SELECT id, email, status, role, storage_quota_bytes, storage_used_bytes,
		balance_credits, frozen_credits, COALESCE(upstream_api_key, ''), created_at, updated_at
		FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Email, &u.Status, &u.Role, &u.StorageQuotaBytes, &u.StorageUsedBytes,
			&u.BalanceCredits, &u.FrozenCredits, &u.UpstreamAPIKey, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// AddStorage 无条件地按 delta 调整 storage_used_bytes。想要强制执行配额上限的调用方必须在调用前
// 显式做预检查（StorageUsage → 比较 used+delta 与 quota）。
func (r *SQLiteRepository) AddStorage(id int64, delta int64) error {
	_, err := r.db.Exec(`UPDATE users SET storage_used_bytes = storage_used_bytes + ? WHERE id = ?`, delta, id)
	return err
}

// AddStorageTx 在调用方事务里累计用量（与 asset 行插入同事务，生成产物落库用）。
func (r *SQLiteRepository) AddStorageTx(tx *sql.Tx, id int64, delta int64) error {
	_, err := tx.Exec(`UPDATE users SET storage_used_bytes = storage_used_bytes + ? WHERE id = ?`, delta, id)
	return err
}

// StorageUsage 一条 SELECT 取回 (used, quota)，供各上传入口做配额预检。
// 本地用户的 quota 是迁移预置的 1 PiB，预检实际上永远放行。
func (r *SQLiteRepository) StorageUsage(id int64) (int64, int64, error) {
	var used, quota int64
	if err := r.db.QueryRow(`SELECT storage_used_bytes, storage_quota_bytes FROM users WHERE id = ?`, id).Scan(&used, &quota); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, ErrNotFound
		}
		return 0, 0, err
	}
	return used, quota, nil
}

// ComputeStorageUsed 返回某用户权威的存储用量——assets 加上 library_items 的实时 SUM，
// 用于展示，保证这个数字永远不会和计数器漂移。
func (r *SQLiteRepository) ComputeStorageUsed(id int64) (int64, error) {
	var assetsSum, librarySum int64
	if err := r.db.QueryRow(`SELECT IFNULL(SUM(size_bytes), 0) FROM assets WHERE user_id = ?`, id).Scan(&assetsSum); err != nil {
		return 0, err
	}
	if err := r.db.QueryRow(`SELECT IFNULL(SUM(size_bytes), 0) FROM library_items WHERE user_id = ?`, id).Scan(&librarySum); err != nil {
		return 0, err
	}
	return assetsSum + librarySum, nil
}
