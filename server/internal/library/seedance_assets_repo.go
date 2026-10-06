package library

import (
	"database/sql"
	"errors"
	"time"
)

// SeedanceAsset 是一个素材（图片/视频/音频）的 Seedance 合规校验状态，按 object_key 归属。
// object_key 是素材在本系统里的真正身份——画布 assets 和素材库 library_items
// 只是它的两种引用形态（IsStillReferenced 就是拿它同时查这两张表）。所以校验
// 一次，这个素材在画布、素材库、任何项目里都通用，与它放在哪个目录完全无关。
type SeedanceAsset struct {
	ObjectKey string  `json:"objectKey"`
	UserID    int64   `json:"-"`
	AssetID   *string `json:"assetId,omitempty"`
	Version   *int    `json:"-"`
	// AssetType 是上游 AssetType 原值（Image/Video/Audio）。worker 重试提交时要重放
	// 它，所以必须持久化——那一刻没有前端上下文（见 0084）。
	AssetType     string     `json:"assetType"`
	Status        string     `json:"status"`
	Error         *string    `json:"error,omitempty"`
	FirstFailedAt *time.Time `json:"-"`
}

var ErrSeedanceAssetNotFound = errors.New("seedance asset not found")

// SeedanceAssetsRepository 是校验状态的持久化边界。
type SeedanceAssetsRepository interface {
	Get(objectKey string) (*SeedanceAsset, error)
	GetMany(userID int64, objectKeys []string) (map[string]SeedanceAsset, error)
	// Upsert 提交校验时写入/重置一行（status=pending，清空上一轮的错误与失败计时）。
	Upsert(a SeedanceAsset) error
	SetStatus(objectKey, status string) error
	// SetError 落失败状态 + 原因，并记录本轮首次失败时间（已有值保持不变）——
	// 它驱动 service 层的 1 小时重试兜底窗口。
	SetError(objectKey, status, errMsg string) error
	// ListPending 取所有待轮询的行（pending 或可重试的 failed），供 worker 收敛。
	ListPending(currentVersion int) ([]SeedanceAsset, error)
}

type SQLiteSeedanceAssetsRepository struct{ db *sql.DB }

func NewSQLiteSeedanceAssetsRepository(db *sql.DB) *SQLiteSeedanceAssetsRepository {
	return &SQLiteSeedanceAssetsRepository{db: db}
}

const seedanceAssetCols = `object_key, user_id, asset_id, version, asset_type, status, error, first_failed_at`

func scanSeedanceAsset(row interface{ Scan(...any) error }) (SeedanceAsset, error) {
	var a SeedanceAsset
	var assetID, errMsg sql.NullString
	var version sql.NullInt64
	var firstFailed sql.NullTime
	if err := row.Scan(&a.ObjectKey, &a.UserID, &assetID, &version, &a.AssetType, &a.Status, &errMsg, &firstFailed); err != nil {
		return a, err
	}
	if assetID.Valid {
		v := assetID.String
		a.AssetID = &v
	}
	if version.Valid {
		v := int(version.Int64)
		a.Version = &v
	}
	if errMsg.Valid {
		v := errMsg.String
		a.Error = &v
	}
	if firstFailed.Valid {
		v := firstFailed.Time
		a.FirstFailedAt = &v
	}
	return a, nil
}

func (r *SQLiteSeedanceAssetsRepository) Get(objectKey string) (*SeedanceAsset, error) {
	row := r.db.QueryRow(`SELECT `+seedanceAssetCols+` FROM seedance_assets WHERE object_key=?`, objectKey)
	a, err := scanSeedanceAsset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSeedanceAssetNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// GetMany 批量取状态，供前端一次性给多个画布节点上角标。
func (r *SQLiteSeedanceAssetsRepository) GetMany(userID int64, objectKeys []string) (map[string]SeedanceAsset, error) {
	out := map[string]SeedanceAsset{}
	if len(objectKeys) == 0 {
		return out, nil
	}
	q := `SELECT ` + seedanceAssetCols + ` FROM seedance_assets WHERE user_id=? AND object_key IN (?` +
		repeatPlaceholders(len(objectKeys)-1) + `)`
	args := make([]any, 0, len(objectKeys)+1)
	args = append(args, userID)
	for _, k := range objectKeys {
		args = append(args, k)
	}
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanSeedanceAsset(rows)
		if err != nil {
			return nil, err
		}
		out[a.ObjectKey] = a
	}
	return out, rows.Err()
}

func repeatPlaceholders(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += ",?"
	}
	return s
}

func (r *SQLiteSeedanceAssetsRepository) Upsert(a SeedanceAsset) error {
	_, err := r.db.Exec(`
        INSERT INTO seedance_assets (object_key, user_id, asset_id, version, asset_type, status, error, first_failed_at)
        VALUES (?, ?, ?, ?, ?, ?, NULL, NULL)
        ON CONFLICT (object_key) DO UPDATE SET
            asset_id = excluded.asset_id, version = excluded.version, asset_type = excluded.asset_type,
            status = excluded.status, error = NULL, first_failed_at = NULL`,
		a.ObjectKey, a.UserID, strPtrArg(a.AssetID), intPtrArg(a.Version), a.AssetType, a.Status)
	return err
}

// strPtrArg / intPtrArg 把可空字段显式转成驱动参数（nil → SQL NULL），不依赖驱动对指针的处理。
func strPtrArg(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func intPtrArg(p *int) any {
	if p == nil {
		return nil
	}
	return int64(*p)
}

func (r *SQLiteSeedanceAssetsRepository) SetStatus(objectKey, status string) error {
	_, err := r.db.Exec(
		`UPDATE seedance_assets SET status=?, error=NULL, first_failed_at=NULL WHERE object_key=?`,
		status, objectKey)
	return err
}

func (r *SQLiteSeedanceAssetsRepository) SetError(objectKey, status, errMsg string) error {
	var e any // nil → SQL NULL
	if errMsg != "" {
		e = errMsg
	}
	_, err := r.db.Exec(
		`UPDATE seedance_assets SET status=?, error=?, first_failed_at=COALESCE(first_failed_at, CURRENT_TIMESTAMP) WHERE object_key=?`,
		status, e, objectKey)
	return err
}

// ListPending：待收敛的行 = 还没拿到当前版本 active 结果的。
// 终态 failed_permanent 不进待办（同图重试必然同样结果）；failed 仍进，
// 由 service 的 1 小时兜底窗口决定何时转终态。
func (r *SQLiteSeedanceAssetsRepository) ListPending(currentVersion int) ([]SeedanceAsset, error) {
	rows, err := r.db.Query(`SELECT `+seedanceAssetCols+` FROM seedance_assets
        WHERE status <> 'failed_permanent'
          AND (status <> 'active' OR version IS NULL OR version <> ?)`, currentVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeedanceAsset
	for rows.Next() {
		a, err := scanSeedanceAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
