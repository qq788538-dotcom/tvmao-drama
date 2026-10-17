package library

import (
	"database/sql"
	"errors"
	"time"
)

var ErrFolderNotFound = errors.New("library folder not found")
var ErrFolderNotEmpty = errors.New("library folder not empty")
var ErrFolderCycle = errors.New("library folder parent_id forms a cycle")

type Folder struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"-"`
	ParentID  *int64    `json:"parentId,omitempty"`
	Name      string    `json:"name"`
	ItemCount int64     `json:"itemCount"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type FoldersRepository interface {
	Create(userID int64, parentID *int64, name string) (int64, error)
	List(userID int64) ([]Folder, error)
	Get(userID, id int64) (*Folder, error)
	Rename(userID, id int64, name string) error
	Move(userID, id int64, newParentID *int64) error
	DeleteIfEmpty(userID, id int64) error
	DescendantIDs(userID, id int64) ([]int64, error) // 用于 force=1 递归删
}

type SQLiteFoldersRepository struct{ db *sql.DB }

func NewSQLiteFoldersRepository(db *sql.DB) *SQLiteFoldersRepository {
	return &SQLiteFoldersRepository{db: db}
}

func (r *SQLiteFoldersRepository) Create(userID int64, parentID *int64, name string) (int64, error) {
	var pid any
	if parentID != nil {
		// 防止跨用户 parent
		var owner int64
		if err := r.db.QueryRow(`SELECT user_id FROM library_folders WHERE id=?`, *parentID).Scan(&owner); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return 0, ErrFolderNotFound
			}
			return 0, err
		}
		if owner != userID {
			return 0, ErrFolderNotFound
		}
		pid = *parentID
	}
	res, err := r.db.Exec(`INSERT INTO library_folders (user_id, parent_id, name) VALUES (?, ?, ?)`, userID, pid, name)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *SQLiteFoldersRepository) List(userID int64) ([]Folder, error) {
	rows, err := r.db.Query(`
        SELECT f.id, f.user_id, f.parent_id, f.name, f.created_at, f.updated_at,
               (SELECT COUNT(*) FROM library_items i WHERE i.folder_id = f.id) AS item_count
          FROM library_folders f
         WHERE f.user_id = ?
         ORDER BY f.parent_id, f.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Folder
	for rows.Next() {
		var f Folder
		var parent sql.NullInt64
		if err := rows.Scan(&f.ID, &f.UserID, &parent, &f.Name, &f.CreatedAt, &f.UpdatedAt, &f.ItemCount); err != nil {
			return nil, err
		}
		if parent.Valid {
			p := parent.Int64
			f.ParentID = &p
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *SQLiteFoldersRepository) Get(userID, id int64) (*Folder, error) {
	row := r.db.QueryRow(`
        SELECT id, user_id, parent_id, name, created_at, updated_at,
               (SELECT COUNT(*) FROM library_items WHERE folder_id = library_folders.id) AS item_count
          FROM library_folders
         WHERE id=? AND user_id=?`, id, userID)
	var f Folder
	var parent sql.NullInt64
	if err := row.Scan(&f.ID, &f.UserID, &parent, &f.Name, &f.CreatedAt, &f.UpdatedAt, &f.ItemCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFolderNotFound
		}
		return nil, err
	}
	if parent.Valid {
		p := parent.Int64
		f.ParentID = &p
	}
	return &f, nil
}

func (r *SQLiteFoldersRepository) Rename(userID, id int64, name string) error {
	res, err := r.db.Exec(`UPDATE library_folders SET name=? WHERE id=? AND user_id=?`, name, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrFolderNotFound
	}
	return nil
}

func (r *SQLiteFoldersRepository) Move(userID, id int64, newParentID *int64) error {
	// 查环与 UPDATE 必须在同一个事务里：_txlock=immediate 下 Begin 即拿写锁，
	// 并发的两个 Move（A 挂到 B 下、B 挂到 A 下）串行执行，后一个必然看到环。
	// 分开执行时两边都能通过查环，造出环后 DescendantIDs 的 BFS 会死循环。
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 循环检测：从 newParentID 向上爬，不能爬到 id
	if newParentID != nil {
		cur := *newParentID
		for {
			if cur == id {
				return ErrFolderCycle
			}
			var p sql.NullInt64
			if err := tx.QueryRow(`SELECT parent_id FROM library_folders WHERE id=? AND user_id=?`, cur, userID).Scan(&p); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrFolderNotFound
				}
				return err
			}
			if !p.Valid {
				break
			}
			cur = p.Int64
		}
	}
	var pid any
	if newParentID != nil {
		pid = *newParentID
	}
	res, err := tx.Exec(`UPDATE library_folders SET parent_id=? WHERE id=? AND user_id=?`, pid, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrFolderNotFound
	}
	return tx.Commit()
}

func (r *SQLiteFoldersRepository) DeleteIfEmpty(userID, id int64) error {
	var n int64
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE folder_id=? AND user_id=?`, id, userID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrFolderNotEmpty
	}
	var children int64
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM library_folders WHERE parent_id=? AND user_id=?`, id, userID).Scan(&children); err != nil {
		return err
	}
	if children > 0 {
		return ErrFolderNotEmpty
	}
	res, err := r.db.Exec(`DELETE FROM library_folders WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrFolderNotFound
	}
	return nil
}

func (r *SQLiteFoldersRepository) DescendantIDs(userID, id int64) ([]int64, error) {
	out := []int64{id}
	queue := []int64{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		rows, err := r.db.Query(`SELECT id FROM library_folders WHERE parent_id=? AND user_id=?`, cur, userID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c int64
			if err := rows.Scan(&c); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, c)
			queue = append(queue, c)
		}
		rows.Close()
	}
	return out, nil
}
