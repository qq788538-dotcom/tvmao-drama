package library

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

var ErrItemNotFound = errors.New("library item not found")

type Item struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"-"`
	FolderID   int64     `json:"folderId"`
	ObjectKey  string    `json:"-"` // 后端内部用，不暴露
	URL        string    `json:"url"`
	SizeBytes  int64     `json:"sizeBytes"`
	MimeType   string    `json:"mimeType"`
	Kind       string    `json:"kind"`
	Width      *int      `json:"width,omitempty"`
	Height     *int      `json:"height,omitempty"`
	DurationMs *int64    `json:"durationMs,omitempty"`
	Source     string    `json:"source"`
	Name       string    `json:"name,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

type ItemFilter struct {
	UserID   int64
	FolderID int64 // 0 表示不过滤
	Search   string
	Limit    int
	Offset   int
}

type ItemsRepository interface {
	Insert(rec Item) (int64, error)
	Get(userID, id int64) (*Item, error)
	List(f ItemFilter) ([]Item, int64, error)
	Update(userID, id int64, name *string, folderID *int64) error
	Delete(userID, id int64) error
	ListByFolderIDs(userID int64, folderIDs []int64) ([]Item, error)
	CountForFolder(userID, folderID int64) (int64, error)
}

type SQLiteItemsRepository struct{ db *sql.DB }

func NewSQLiteItemsRepository(db *sql.DB) *SQLiteItemsRepository {
	return &SQLiteItemsRepository{db: db}
}

func scanItem(row interface{ Scan(...any) error }) (Item, error) {
	var it Item
	var w, h sql.NullInt64
	var d sql.NullInt64
	var name sql.NullString
	err := row.Scan(&it.ID, &it.UserID, &it.FolderID, &it.ObjectKey, &it.URL,
		&it.SizeBytes, &it.MimeType, &it.Kind, &w, &h, &d, &it.Source, &name, &it.CreatedAt)
	if err != nil {
		return it, err
	}
	if w.Valid {
		v := int(w.Int64)
		it.Width = &v
	}
	if h.Valid {
		v := int(h.Int64)
		it.Height = &v
	}
	if d.Valid {
		v := d.Int64
		it.DurationMs = &v
	}
	if name.Valid {
		it.Name = name.String
	}
	return it, nil
}

const itemCols = `id, user_id, folder_id, object_key, url, size_bytes, mime_type, kind, width, height, duration_ms, source, name, created_at`

func (r *SQLiteItemsRepository) Insert(rec Item) (int64, error) {
	res, err := r.db.Exec(`
        INSERT INTO library_items (user_id, folder_id, object_key, url, size_bytes, mime_type, kind, width, height, duration_ms, source, name)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.UserID, rec.FolderID, rec.ObjectKey, rec.URL, rec.SizeBytes, rec.MimeType, rec.Kind,
		nullableInt(rec.Width), nullableInt(rec.Height), nullableInt64(rec.DurationMs), rec.Source, nullableString(rec.Name))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func nullableInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
func nullableInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r *SQLiteItemsRepository) Get(userID, id int64) (*Item, error) {
	row := r.db.QueryRow(`SELECT `+itemCols+` FROM library_items WHERE id=? AND user_id=?`, id, userID)
	it, err := scanItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrItemNotFound
		}
		return nil, err
	}
	return &it, nil
}

func (r *SQLiteItemsRepository) List(f ItemFilter) ([]Item, int64, error) {
	where := `WHERE user_id=?`
	args := []any{f.UserID}
	if f.FolderID > 0 {
		where += ` AND folder_id=?`
		args = append(args, f.FolderID)
	}
	if f.Search != "" {
		where += ` AND name LIKE ?`
		args = append(args, "%"+f.Search+"%")
	}
	var total int64
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM library_items `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	// 在追加 limit/offset 之前先拷贝 args —— 避免 args 恰好有富余容量时发生
	// 切片别名共享，否则会在将来复用时悄悄破坏上面的 count 查询。
	args2 := make([]any, len(args), len(args)+2)
	copy(args2, args)
	args2 = append(args2, f.Limit, f.Offset)
	rows, err := r.db.Query(`SELECT `+itemCols+` FROM library_items `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, args2...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

func (r *SQLiteItemsRepository) Update(userID, id int64, name *string, folderID *int64) error {
	sets := []string{}
	args := []any{}
	if name != nil {
		sets = append(sets, "name=?")
		args = append(args, nullableString(*name))
	}
	if folderID != nil {
		// 校验目标 folder 属于同一个用户。没有这步的话，用户可以通过公开 API
		// 把自己的 item 移动到别人的 folder 里。
		var folderOwner int64
		if err := r.db.QueryRow(`SELECT user_id FROM library_folders WHERE id=?`, *folderID).Scan(&folderOwner); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrFolderNotFound
			}
			return err
		}
		if folderOwner != userID {
			return ErrFolderNotFound
		}
		sets = append(sets, "folder_id=?")
		args = append(args, *folderID)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id, userID)
	q := "UPDATE library_items SET " + strings.Join(sets, ", ") + " WHERE id=? AND user_id=?"
	res, err := r.db.Exec(q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrItemNotFound
	}
	return nil
}

func (r *SQLiteItemsRepository) Delete(userID, id int64) error {
	res, err := r.db.Exec(`DELETE FROM library_items WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrItemNotFound
	}
	return nil
}

func (r *SQLiteItemsRepository) ListByFolderIDs(userID int64, folderIDs []int64) ([]Item, error) {
	if len(folderIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(folderIDs))
	args := []any{userID}
	for i, id := range folderIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	qms := strings.Join(placeholders, ",")
	rows, err := r.db.Query(`SELECT `+itemCols+` FROM library_items WHERE user_id=? AND folder_id IN (`+qms+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (r *SQLiteItemsRepository) CountForFolder(userID, folderID int64) (int64, error) {
	var n int64
	err := r.db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE folder_id=? AND user_id=?`, folderID, userID).Scan(&n)
	return n, err
}
