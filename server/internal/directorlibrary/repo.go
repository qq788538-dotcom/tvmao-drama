// Package directorlibrary 是「3D 导演台·后台全局模型库」的数据层：管理员配置的
// 公共 3D 模型目录（director_library_categories / director_library_models 两张表，
// schema 见桌面端迁移 0012_p1d_director.sql，源自参考仓 0093），用户端只读。
//
// 与 directormodels（用户级模型库）平行不合并：那边是用户私有资产（计存储配额、
// 归属校验、按 user 扫引用），这边是平台资产（不占任何用户配额、admin 维护、
// 引用检查扫**全库**所有用户的快照）。两者只共享「同一类文件」带来的格式约束
// 与 media:// 引用形态，所以校验函数与 LIKE 构造直接复用 directormodels 的实现
// （见 service.go 的别名），其余数据语义各管各的。
package directorlibrary

import (
	"database/sql"
	"errors"
	"time"
)

var ErrNotFound = errors.New("director library row not found")

type Category struct {
	ID        int64
	Name      string
	SortOrder int
	Enabled   bool
	// ModelCount 是分类下的模型条数（含已下架的——后台视角要看全量；
	// 也是「非空分类拒删」判定的口径：下架的模型行仍在，分类照样不许删）。
	ModelCount int64
	CreatedAt  time.Time
}

type Model struct {
	ID         int64
	CategoryID int64
	Name       string
	FileName   string
	ObjectKey  string
	// ThumbObjectKey 空串 = 无缩略图（DB 里存 NULL，读写各转一次）。
	ThumbObjectKey string
	SizeBytes      int64
	MimeType       string
	SortOrder      int
	Enabled        bool
	CreatedAt      time.Time
}

// CategoryWithModels 是用户端目录的展平结果：enabled 分类带各自 enabled 模型。
type CategoryWithModels struct {
	Category
	Models []Model
}

// Execer 让 DeleteModel 既能用 *sql.DB 也能用 *sql.Tx——真删除必须和
// 「标 orphan」在同一个事务里（handler 侧组合），套路同 directormodels.Execer。
type Execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type Repository interface {
	// ListCategories 后台全量（含停用），按 sort_order,id 排序，带 ModelCount。
	ListCategories() ([]Category, error)
	GetCategory(id int64) (*Category, error)
	CreateCategory(c Category) (int64, error)
	UpdateCategory(c Category) error
	// DeleteCategory 只做删行；「非空拒删」的业务判定在 Service.DeleteCategory
	// （FK RESTRICT 是数据库层最后一道兜底，不该被正常路径触发）。
	DeleteCategory(id int64) error
	CountModels(categoryID int64) (int64, error)

	// ListModels 后台列表；categoryID<=0 表示不过滤分类。
	ListModels(categoryID int64) ([]Model, error)
	GetModel(id int64) (*Model, error)
	CreateModel(m Model) (int64, error)
	UpdateModel(ex Execer, m Model) error
	DeleteModel(ex Execer, id int64) error

	// ListEnabledJoined 出用户端目录：仅 enabled 分类与 enabled 模型，
	// 分类与分类内模型都按 sort_order,id 排序。空分类保留（models 为空数组），
	// 展不展示由前端定——后端少做一层过滤就少一处口径分歧。
	ListEnabledJoined() ([]CategoryWithModels, error)

	// CountProjectsReferencingAllUsers 数「画布快照 payload 出现
	// media://<objectKey> 引用」的未软删项目数——**不过滤 user**：平台资产
	// 任何用户都可能引用，真删除前必须扫全库（对比 directormodels 的
	// CountProjectsReferencing 只扫模型主人自己的项目）。
	CountProjectsReferencingAllUsers(objectKey string) (int64, error)
}

type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

// categoryColumns 带 ModelCount 子查询：后台列表/单查都要展示条数，
// 顺手也是「非空拒删」预检的数据源，单独跑 COUNT 反而两套口径。
const categoryColumns = `
    c.id, c.name, c.sort_order, c.enabled, c.created_at,
    (SELECT COUNT(*) FROM director_library_models m WHERE m.category_id = c.id)`

func scanCategory(row interface{ Scan(...any) error }) (Category, error) {
	var c Category
	err := row.Scan(&c.ID, &c.Name, &c.SortOrder, &c.Enabled, &c.CreatedAt, &c.ModelCount)
	return c, err
}

func (r *SQLiteRepository) ListCategories() ([]Category, error) {
	rows, err := r.db.Query(`
        SELECT ` + categoryColumns + `
        FROM director_library_categories c
        ORDER BY c.sort_order, c.id
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) GetCategory(id int64) (*Category, error) {
	c, err := scanCategory(r.db.QueryRow(`
        SELECT `+categoryColumns+`
        FROM director_library_categories c WHERE c.id = ?
    `, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *SQLiteRepository) CreateCategory(c Category) (int64, error) {
	res, err := r.db.Exec(`
        INSERT INTO director_library_categories (name, sort_order, enabled)
        VALUES (?, ?, ?)
    `, c.Name, c.SortOrder, c.Enabled)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *SQLiteRepository) UpdateCategory(c Category) error {
	_, err := r.db.Exec(`
        UPDATE director_library_categories SET name = ?, sort_order = ?, enabled = ?
        WHERE id = ?
    `, c.Name, c.SortOrder, c.Enabled, c.ID)
	return err
}

func (r *SQLiteRepository) DeleteCategory(id int64) error {
	_, err := r.db.Exec(`DELETE FROM director_library_categories WHERE id = ?`, id)
	return err
}

func (r *SQLiteRepository) CountModels(categoryID int64) (int64, error) {
	var n int64
	err := r.db.QueryRow(`SELECT COUNT(*) FROM director_library_models WHERE category_id = ?`, categoryID).Scan(&n)
	return n, err
}

const modelColumns = `
    id, category_id, name, file_name, object_key, COALESCE(thumb_object_key, ''),
    size_bytes, mime_type, sort_order, enabled, created_at`

func scanModel(row interface{ Scan(...any) error }) (Model, error) {
	var m Model
	err := row.Scan(&m.ID, &m.CategoryID, &m.Name, &m.FileName, &m.ObjectKey,
		&m.ThumbObjectKey, &m.SizeBytes, &m.MimeType, &m.SortOrder, &m.Enabled, &m.CreatedAt)
	return m, err
}

func (r *SQLiteRepository) ListModels(categoryID int64) ([]Model, error) {
	q := `SELECT ` + modelColumns + ` FROM director_library_models`
	args := []any{}
	if categoryID > 0 {
		q += ` WHERE category_id = ?`
		args = append(args, categoryID)
	}
	// 后台列表按分类内展示序排，与用户端同一顺序——所见即用户所得。
	q += ` ORDER BY category_id, sort_order, id`
	rows, err := r.db.Query(q, args...)
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

func (r *SQLiteRepository) GetModel(id int64) (*Model, error) {
	m, err := scanModel(r.db.QueryRow(`
        SELECT `+modelColumns+` FROM director_library_models WHERE id = ?
    `, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *SQLiteRepository) CreateModel(m Model) (int64, error) {
	// NULLIF：空串缩略图落 NULL，列上的「可空=无缩略图」语义只在这一处转换。
	res, err := r.db.Exec(`
        INSERT INTO director_library_models
            (category_id, name, file_name, object_key, thumb_object_key, size_bytes, mime_type, sort_order, enabled)
        VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?)
    `, m.CategoryID, m.Name, m.FileName, m.ObjectKey, m.ThumbObjectKey,
		m.SizeBytes, m.MimeType, m.SortOrder, m.Enabled)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *SQLiteRepository) UpdateModel(ex Execer, m Model) error {
	_, err := ex.Exec(`
        UPDATE director_library_models SET
            category_id = ?, name = ?, file_name = ?, object_key = ?,
            thumb_object_key = NULLIF(?, ''), size_bytes = ?, mime_type = ?,
            sort_order = ?, enabled = ?
        WHERE id = ?
    `, m.CategoryID, m.Name, m.FileName, m.ObjectKey, m.ThumbObjectKey,
		m.SizeBytes, m.MimeType, m.SortOrder, m.Enabled, m.ID)
	return err
}

func (r *SQLiteRepository) DeleteModel(ex Execer, id int64) error {
	_, err := ex.Exec(`DELETE FROM director_library_models WHERE id = ?`, id)
	return err
}

func (r *SQLiteRepository) ListEnabledJoined() ([]CategoryWithModels, error) {
	// LEFT JOIN 保空分类；模型侧的 enabled 过滤必须放 JOIN 条件而不是 WHERE——
	// 放 WHERE 会把「只有下架模型的分类」整行过滤掉，变成隐式隐藏空分类。
	rows, err := r.db.Query(`
        SELECT c.id, c.name,
               m.id, m.name, m.file_name, m.object_key, COALESCE(m.thumb_object_key, '')
        FROM director_library_categories c
        LEFT JOIN director_library_models m
            ON m.category_id = c.id AND m.enabled = 1
        WHERE c.enabled = 1
        ORDER BY c.sort_order, c.id, m.sort_order, m.id
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CategoryWithModels
	for rows.Next() {
		var (
			catID                      int64
			catName                    string
			mID                        sql.NullInt64
			mName, mFile, mKey, mThumb sql.NullString
		)
		if err := rows.Scan(&catID, &catName, &mID, &mName, &mFile, &mKey, &mThumb); err != nil {
			return nil, err
		}
		// 行按分类聚簇（ORDER BY 先分类后模型），首次见到某分类时开新组即可。
		if len(out) == 0 || out[len(out)-1].ID != catID {
			out = append(out, CategoryWithModels{
				Category: Category{ID: catID, Name: catName},
				Models:   []Model{},
			})
		}
		if mID.Valid {
			last := &out[len(out)-1]
			last.Models = append(last.Models, Model{
				ID: mID.Int64, CategoryID: catID, Name: mName.String,
				FileName: mFile.String, ObjectKey: mKey.String, ThumbObjectKey: mThumb.String,
			})
		}
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) CountProjectsReferencingAllUsers(objectKey string) (int64, error) {
	// workflow_snapshots 与 projects 一对一（project_id 是主键），数快照行就是
	// 数项目数。**不过滤 user_id**——平台模型谁都可能引用；只排软删项目
	// （deleted_at IS NULL）：软删的正在等后台清理，不该拦住真删除。
	var n int64
	err := r.db.QueryRow(`
        SELECT COUNT(*)
        FROM workflow_snapshots ws
        JOIN projects p ON p.id = ws.project_id
        WHERE p.deleted_at IS NULL
          AND ws.payload LIKE ? ESCAPE '\'
    `, ReferenceLikePattern(objectKey)).Scan(&n)
	return n, err
}
