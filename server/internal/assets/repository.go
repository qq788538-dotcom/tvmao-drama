package assets

import (
	"database/sql"
	"errors"
	"time"
)

type AssetRecord struct {
	ID            int64     `json:"id"`
	UserID        int64     `json:"userId"`
	ProjectID     int64     `json:"projectId,omitempty"`    // 0 → DB 里为 NULL（该 asset 未挂到任何 project）
	SourceTaskID  int64     `json:"sourceTaskId,omitempty"` // 0 → NULL；生成任务的产物会设上它，方便按任务清理
	ObjectKey     string    `json:"objectKey"`
	SourceKind    string    `json:"sourceKind"`
	URL           string    `json:"url"`
	SizeBytes     int64     `json:"sizeBytes"`
	MimeType      string    `json:"mimeType"`
	ContentHash   string    `json:"contentHash,omitempty"` // sha256 十六进制；"" → DB 里为 NULL（历史遗留行）
	CreatedAt     time.Time `json:"createdAt"`
	LibraryItemID int64     `json:"libraryItemId,omitempty"` // 0 → NULL
	DurationMs    int64     `json:"durationMs,omitempty"`    // 视频真实时长（毫秒）；0 → DB NULL。仅视频有值
}

type Repository interface {
	Insert(rec AssetRecord) (int64, error)
	ListForUser(userID int64, limit int) ([]AssetRecord, error)
	ListForProject(projectID int64) ([]AssetRecord, error)
	DeleteForProject(projectID int64) (int64, error)
	ListForTask(taskID int64) ([]AssetRecord, error)
	DeleteForTask(taskID int64) (int64, error)
	ListByObjectKey(objectKey string) ([]AssetRecord, error)
	ClearLibraryRef(libraryItemID int64) error
	// FindByUserProjectHash 返回 `userID` 在 `projectID` 下、内容 sha256 命中给定
	// hash 的最近一条 asset。传 projectID=0 来匹配没挂到任何 project 的 asset。
	// 上传路径用它来跳过重复的 TOS 写入——当用户在同一个 project 内已经拥有相同
	// 字节时。跨 project / 跨用户的去重这里故意不做——那需要一张带引用计数的 blobs
	// 表，否则会破坏按 project 的存储计量和删 project 时的清理。
	// 没有命中时返回 (nil, sql.ErrNoRows)。
	FindByUserProjectHash(userID, projectID int64, hash string) (*AssetRecord, error)
	// FindByTaskSlot 按 (source_task_id, output_index) 查槽位(v5 §5.5):persistOutputs
	// 重放先查——命中直接复用 asset,不下载不上传。未命中返回 sql.ErrNoRows。
	FindByTaskSlot(taskID int64, outputIndex int) (*AssetRecord, error)
	// InsertSlotTx 在调用方事务里按槽位插入。唯一键 uk_assets_task_output 兜底并发重放:
	// 撞键时 ON CONFLICT DO NOTHING，再在同一事务里按槽位查回已有行 id,inserted=false——调用方据此
	// 决定是否累计存储用量(只有真正新插入才 AddStorageTx,同一事务)。
	InsertSlotTx(tx *sql.Tx, rec AssetRecord, outputIndex int) (id int64, inserted bool, err error)
	// GetDurationMsByObjectKey 查该 object_key 最新一行(id 最大)的 duration_ms。
	// 供去字幕计费读服务端权威时长用。无行 → (0,false,nil)；
	// 行在但 duration_ms 为 NULL(历史遗留/尚未回填) → (0,true,nil)；
	// 有值 → (值,true,nil)。
	GetDurationMsByObjectKey(objectKey string) (durationMs int64, found bool, err error)
	// SetDurationMsByObjectKey 回填该 object_key 下所有行的 duration_ms。
	// 用于 NULL 懒回填——读到 NULL 时用探测到的真实时长补齐，避免下次再探测。
	SetDurationMsByObjectKey(objectKey string, durationMs int64) error
}

type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

// FindByID 按主键取单行。agent 的 read_attachment 工具用它做归属校验后分段
// 读取大文本附件；未命中返回 (nil, nil)——调用方统一报「不存在」，不区分
// 「没有这行」与「不是你的」（不泄露存在性）。
func (r *SQLiteRepository) FindByID(id int64) (*AssetRecord, error) {
	row := r.db.QueryRow(`SELECT id, user_id, COALESCE(project_id,0), object_key, source_kind, url, size_bytes, mime_type, created_at FROM assets WHERE id = ?`, id)
	var rec AssetRecord
	if err := row.Scan(&rec.ID, &rec.UserID, &rec.ProjectID, &rec.ObjectKey, &rec.SourceKind, &rec.URL, &rec.SizeBytes, &rec.MimeType, &rec.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

func (r *SQLiteRepository) FindByTaskSlot(taskID int64, outputIndex int) (*AssetRecord, error) {
	row := r.db.QueryRow(`SELECT id, user_id, IFNULL(project_id,0), IFNULL(source_task_id,0), object_key, source_kind, url, size_bytes, mime_type FROM assets WHERE source_task_id=? AND output_index=?`, taskID, outputIndex)
	var rec AssetRecord
	if err := row.Scan(&rec.ID, &rec.UserID, &rec.ProjectID, &rec.SourceTaskID, &rec.ObjectKey, &rec.SourceKind, &rec.URL, &rec.SizeBytes, &rec.MimeType); err != nil {
		return nil, err
	}
	return &rec, nil
}

// InsertSlotTx 见接口注释。SQLite 没有 MySQL 的 LAST_INSERT_ID(id) 技巧：撞键时 DO NOTHING，
// RowsAffected()==0 说明槽位已被（并发）重放写过，在同一事务里查回已有行的 id。
func (r *SQLiteRepository) InsertSlotTx(tx *sql.Tx, rec AssetRecord, outputIndex int) (int64, bool, error) {
	var projectID, sourceTaskID, durationMs any
	if rec.ProjectID > 0 {
		projectID = rec.ProjectID
	}
	if rec.SourceTaskID > 0 {
		sourceTaskID = rec.SourceTaskID
	}
	if rec.DurationMs > 0 {
		durationMs = rec.DurationMs
	}
	res, err := tx.Exec(
		`INSERT INTO assets (user_id, project_id, source_task_id, output_index, object_key, source_kind, url, size_bytes, mime_type, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (source_task_id, output_index) DO NOTHING`,
		rec.UserID, projectID, sourceTaskID, outputIndex, rec.ObjectKey, rec.SourceKind, rec.URL, rec.SizeBytes, rec.MimeType, durationMs,
	)
	if err != nil {
		return 0, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if n == 1 {
		id, err := res.LastInsertId()
		if err != nil {
			return 0, false, err
		}
		return id, true, nil
	}
	var id int64
	if err := tx.QueryRow(`SELECT id FROM assets WHERE source_task_id = ? AND output_index = ?`, sourceTaskID, outputIndex).Scan(&id); err != nil {
		return 0, false, err
	}
	return id, false, nil
}

func (r *SQLiteRepository) Insert(rec AssetRecord) (int64, error) {
	var projectID, sourceTaskID, libraryItemID, contentHash any
	if rec.ProjectID > 0 {
		projectID = rec.ProjectID
	}
	if rec.SourceTaskID > 0 {
		sourceTaskID = rec.SourceTaskID
	}
	if rec.LibraryItemID > 0 {
		libraryItemID = rec.LibraryItemID
	}
	if rec.ContentHash != "" {
		contentHash = rec.ContentHash
	}
	var durationMs any
	if rec.DurationMs > 0 {
		durationMs = rec.DurationMs
	}
	res, err := r.db.Exec(
		`INSERT INTO assets (user_id, project_id, source_task_id, object_key, source_kind, url, size_bytes, mime_type, library_item_id, content_hash, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.UserID, projectID, sourceTaskID, rec.ObjectKey, rec.SourceKind, rec.URL, rec.SizeBytes, rec.MimeType, libraryItemID, contentHash, durationMs,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FindByUserProjectHash 返回 (userID, projectID) 下 sha256 hash 命中的最近一条
// asset，没有命中时返回 sql.ErrNoRows。
// 传 projectID=0 来查没挂到任何 project 的 asset。
func (r *SQLiteRepository) FindByUserProjectHash(userID, projectID int64, hash string) (*AssetRecord, error) {
	if hash == "" {
		return nil, sql.ErrNoRows
	}
	var (
		rec                                      AssetRecord
		dbProjectID, sourceTaskID, libraryItemID sql.NullInt64
		err                                      error
	)
	if projectID > 0 {
		err = r.db.QueryRow(
			`SELECT id, user_id, project_id, source_task_id, object_key, source_kind, url, size_bytes, mime_type, library_item_id, content_hash, created_at
			   FROM assets
			  WHERE user_id = ? AND project_id = ? AND content_hash = ?
			  ORDER BY id DESC
			  LIMIT 1`,
			userID, projectID, hash,
		).Scan(&rec.ID, &rec.UserID, &dbProjectID, &sourceTaskID, &rec.ObjectKey, &rec.SourceKind, &rec.URL, &rec.SizeBytes, &rec.MimeType, &libraryItemID, &rec.ContentHash, &rec.CreatedAt)
	} else {
		err = r.db.QueryRow(
			`SELECT id, user_id, project_id, source_task_id, object_key, source_kind, url, size_bytes, mime_type, library_item_id, content_hash, created_at
			   FROM assets
			  WHERE user_id = ? AND project_id IS NULL AND content_hash = ?
			  ORDER BY id DESC
			  LIMIT 1`,
			userID, hash,
		).Scan(&rec.ID, &rec.UserID, &dbProjectID, &sourceTaskID, &rec.ObjectKey, &rec.SourceKind, &rec.URL, &rec.SizeBytes, &rec.MimeType, &libraryItemID, &rec.ContentHash, &rec.CreatedAt)
	}
	if err != nil {
		return nil, err
	}
	rec.ProjectID = dbProjectID.Int64
	rec.SourceTaskID = sourceTaskID.Int64
	rec.LibraryItemID = libraryItemID.Int64
	return &rec, nil
}

func (r *SQLiteRepository) ListForProject(projectID int64) ([]AssetRecord, error) {
	rows, err := r.db.Query(`SELECT id, user_id, object_key, source_kind, url, size_bytes, mime_type, created_at FROM assets WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssetRecord
	for rows.Next() {
		var a AssetRecord
		if err := rows.Scan(&a.ID, &a.UserID, &a.ObjectKey, &a.SourceKind, &a.URL, &a.SizeBytes, &a.MimeType, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// deleteSumming 删除 col = id 的全部行，返回被删行的 size_bytes 之和。求和与删除在
// 同一个 immediate 事务里：分两步时，中间并发插入的行会被删掉却不计入返回值，调用方
// 据此扣减的配额就会少扣。求和出错直接返回，不再吞掉错误按 0 处理。
func (r *SQLiteRepository) deleteSumming(col string, id int64) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var totalSize int64
	if err := tx.QueryRow(`SELECT IFNULL(SUM(size_bytes), 0) FROM assets WHERE `+col+` = ?`, id).Scan(&totalSize); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM assets WHERE `+col+` = ?`, id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return totalSize, nil
}

func (r *SQLiteRepository) DeleteForProject(projectID int64) (int64, error) {
	return r.deleteSumming(`project_id`, projectID)
}

func (r *SQLiteRepository) ListForUser(userID int64, limit int) ([]AssetRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.Query(`SELECT id, user_id, object_key, source_kind, url, size_bytes, mime_type, created_at FROM assets WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssetRecord
	for rows.Next() {
		var a AssetRecord
		if err := rows.Scan(&a.ID, &a.UserID, &a.ObjectKey, &a.SourceKind, &a.URL, &a.SizeBytes, &a.MimeType, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *SQLiteRepository) ListForTask(taskID int64) ([]AssetRecord, error) {
	rows, err := r.db.Query(`SELECT id, user_id, object_key, source_kind, url, size_bytes, mime_type, created_at FROM assets WHERE source_task_id = ?`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssetRecord
	for rows.Next() {
		var a AssetRecord
		if err := rows.Scan(&a.ID, &a.UserID, &a.ObjectKey, &a.SourceKind, &a.URL, &a.SizeBytes, &a.MimeType, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *SQLiteRepository) DeleteForTask(taskID int64) (int64, error) {
	return r.deleteSumming(`source_task_id`, taskID)
}

func (r *SQLiteRepository) ListByObjectKey(objectKey string) ([]AssetRecord, error) {
	rows, err := r.db.Query(`SELECT id, user_id, IFNULL(project_id, 0), IFNULL(source_task_id, 0), object_key, source_kind, url, size_bytes, mime_type, IFNULL(library_item_id, 0), created_at FROM assets WHERE object_key=?`, objectKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssetRecord
	for rows.Next() {
		var a AssetRecord
		if err := rows.Scan(&a.ID, &a.UserID, &a.ProjectID, &a.SourceTaskID, &a.ObjectKey, &a.SourceKind, &a.URL, &a.SizeBytes, &a.MimeType, &a.LibraryItemID, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) ClearLibraryRef(libraryItemID int64) error {
	_, err := r.db.Exec(`UPDATE assets SET library_item_id=NULL WHERE library_item_id=?`, libraryItemID)
	return err
}

// GetDurationMsByObjectKey 见接口注释。
func (r *SQLiteRepository) GetDurationMsByObjectKey(objectKey string) (int64, bool, error) {
	var d sql.NullInt64
	err := r.db.QueryRow(`SELECT duration_ms FROM assets WHERE object_key=? ORDER BY id DESC LIMIT 1`, objectKey).Scan(&d)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if !d.Valid {
		return 0, true, nil
	}
	return d.Int64, true, nil
}

// SetDurationMsByObjectKey 见接口注释。
func (r *SQLiteRepository) SetDurationMsByObjectKey(objectKey string, durationMs int64) error {
	_, err := r.db.Exec(`UPDATE assets SET duration_ms=? WHERE object_key=?`, durationMs, objectKey)
	return err
}
