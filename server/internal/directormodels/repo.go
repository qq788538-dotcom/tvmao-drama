// Package directormodels 是「3D 导演台·用户级云端模型库」的数据层：
// 用户上传的 FBX/OBJ 模型文件目录（director_user_models 表，schema 见
// 桌面端迁移 0012_p1d_director.sql，源自参考仓 0092）。模型是用户级（跨画布项目共享）资源，被引用时以
// media://<object_key> 字符串散落在各项目 workflow_snapshots.payload 里，
// 所以引用检查是字符串扫描而不是外键（见 CountProjectsReferencing）。
package directormodels

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

var ErrNotFound = errors.New("director model not found")

type Model struct {
	ID        int64
	UserID    int64
	Name      string
	FileName  string
	ObjectKey string
	SizeBytes int64
	MimeType  string
	CreatedAt time.Time
}

// Execer 让 Delete 既能用 *sql.DB 也能用 *sql.Tx——删行必须和「标 orphan」
// 在同一个事务里（handler 侧组合），套路同 library.Execer。
type Execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type Repository interface {
	Insert(m Model) (int64, error)
	ListByUser(userID int64) ([]Model, error)
	GetByID(id int64) (*Model, error)
	// Delete 带 user_id 条件兜底：即使 handler 归属校验被绕过，也删不到别人的行。
	Delete(ex Execer, userID, id int64) error
	CountByUser(userID int64) (int64, error)
	// CountProjectsReferencing 数「该用户名下未软删项目里，画布快照 payload
	// 出现 media://<objectKey> 引用」的项目数。删除前的引用检查用。
	CountProjectsReferencing(userID int64, objectKey string) (int64, error)
}

// ReferenceLikePattern 构造扫 workflow_snapshots.payload 的 LIKE 模式。
// 快照里持久化的引用是 canonical 的 media://<object_key> 形式（入库前经
// MediaSigner.Canonicalize，签名 URL 不落库），所以匹配这个前缀即可。
// object_key 里的 LIKE 通配符必须转义——key 由文件名派生，下划线几乎必然
// 出现（uniqueUploadFilename 就带 "_"），不转义会把 a_b.fbx 匹配到 aXb.fbx，
// 误报引用导致用户删不掉自己的模型。
// SQLite 的 LIKE 没有默认转义字符，调用方必须写 `ESCAPE '\'`。
func ReferenceLikePattern(objectKey string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(objectKey)
	return "%media://" + escaped + "%"
}

type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

const modelColumns = "id, user_id, name, file_name, object_key, size_bytes, mime_type, created_at"

func scanModel(row interface{ Scan(...any) error }) (Model, error) {
	var m Model
	err := row.Scan(&m.ID, &m.UserID, &m.Name, &m.FileName, &m.ObjectKey,
		&m.SizeBytes, &m.MimeType, &m.CreatedAt)
	return m, err
}

func (r *SQLiteRepository) Insert(m Model) (int64, error) {
	res, err := r.db.Exec(`
        INSERT INTO director_user_models (user_id, name, file_name, object_key, size_bytes, mime_type)
        VALUES (?, ?, ?, ?, ?, ?)
    `, m.UserID, m.Name, m.FileName, m.ObjectKey, m.SizeBytes, m.MimeType)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *SQLiteRepository) ListByUser(userID int64) ([]Model, error) {
	rows, err := r.db.Query(`
        SELECT `+modelColumns+` FROM director_user_models
        WHERE user_id = ? ORDER BY created_at DESC, id DESC
    `, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Model
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) GetByID(id int64) (*Model, error) {
	m, err := scanModel(r.db.QueryRow(`
        SELECT `+modelColumns+` FROM director_user_models WHERE id = ?
    `, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *SQLiteRepository) Delete(ex Execer, userID, id int64) error {
	_, err := ex.Exec(`DELETE FROM director_user_models WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

func (r *SQLiteRepository) CountByUser(userID int64) (int64, error) {
	var n int64
	err := r.db.QueryRow(`SELECT COUNT(*) FROM director_user_models WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

func (r *SQLiteRepository) CountProjectsReferencing(userID int64, objectKey string) (int64, error) {
	// workflow_snapshots 与 projects 一对一（project_id 是主键），所以数快照行
	// 就是数项目数。只看该用户名下未软删（deleted_at IS NULL）的项目：软删
	// 项目正在等后台 worker 清理，不该拦住用户删模型。
	var n int64
	err := r.db.QueryRow(`
        SELECT COUNT(*)
        FROM workflow_snapshots ws
        JOIN projects p ON p.id = ws.project_id
        WHERE p.user_id = ? AND p.deleted_at IS NULL
          AND ws.payload LIKE ? ESCAPE '\'
    `, userID, ReferenceLikePattern(objectKey)).Scan(&n)
	return n, err
}
