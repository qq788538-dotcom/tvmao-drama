package library

import (
	"database/sql"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db"
)

type OrphanObject struct {
	ObjectKey string
	UserID    int64
	SizeBytes int64
	MarkedAt  time.Time
}

type OrphansRepository interface {
	Mark(tx Execer, objectKey string, userID, sizeBytes int64) error
	ListReady(olderThan time.Duration) ([]OrphanObject, error)
	Delete(objectKey string) error
}

// Execer 让 Mark 既能用 *sql.DB 也能用 *sql.Tx
type Execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type SQLiteOrphansRepository struct{ db *sql.DB }

func NewSQLiteOrphansRepository(db *sql.DB) *SQLiteOrphansRepository {
	return &SQLiteOrphansRepository{db: db}
}

func (r *SQLiteOrphansRepository) Mark(tx Execer, objectKey string, userID, sizeBytes int64) error {
	_, err := tx.Exec(`
        INSERT INTO orphan_objects (object_key, user_id, size_bytes)
        VALUES (?, ?, ?)
        ON CONFLICT (object_key) DO UPDATE SET marked_at = CURRENT_TIMESTAMP
    `, objectKey, userID, sizeBytes)
	return err
}

func (r *SQLiteOrphansRepository) ListReady(olderThan time.Duration) ([]OrphanObject, error) {
	cutoff := db.Time(time.Now().Add(-olderThan))
	rows, err := r.db.Query(`SELECT object_key, user_id, size_bytes, marked_at FROM orphan_objects WHERE marked_at < ?`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrphanObject
	for rows.Next() {
		var o OrphanObject
		if err := rows.Scan(&o.ObjectKey, &o.UserID, &o.SizeBytes, &o.MarkedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *SQLiteOrphansRepository) Delete(objectKey string) error {
	_, err := r.db.Exec(`DELETE FROM orphan_objects WHERE object_key=?`, objectKey)
	return err
}
