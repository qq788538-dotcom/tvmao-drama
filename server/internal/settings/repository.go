package settings

import (
	"database/sql"
	"errors"
)

type Repository interface {
	Get(key string) (string, error)
	Set(key, value string) error
	All() (map[string]string, error)
}

type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

func (r *SQLiteRepository) Get(key string) (string, error) {
	var value string
	if err := r.db.QueryRow("SELECT value FROM settings WHERE \"key\" = ?", key).Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return value, nil
}

func (r *SQLiteRepository) Set(key, value string) error {
	_, err := r.db.Exec("INSERT INTO settings (\"key\", value) VALUES (?, ?) ON CONFLICT (\"key\") DO UPDATE SET value = excluded.value", key, value)
	return err
}

func (r *SQLiteRepository) All() (map[string]string, error) {
	rows, err := r.db.Query("SELECT \"key\", value FROM settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
