package library

import (
	"database/sql"
	"errors"
)

// ErrSeedanceGroupUserNotFound：users 表里没有这个用户。
var ErrSeedanceGroupUserNotFound = errors.New("user not found")

// SQLiteSeedanceGroupStore 读写 users 表上的 Seedance 素材组 id 与建组版本号。
// 参考仓这两个方法在 users.Repository 上（users/repository.go:398-413）；P1b 新写的
// users 包只含存储用量方法，所以放在合规校验自己的包里。
type SQLiteSeedanceGroupStore struct{ db *sql.DB }

func NewSQLiteSeedanceGroupStore(db *sql.DB) *SQLiteSeedanceGroupStore {
	return &SQLiteSeedanceGroupStore{db: db}
}

func (s *SQLiteSeedanceGroupStore) GetSeedanceGroup(userID int64) (string, int, error) {
	var gid sql.NullString
	var ver sql.NullInt64
	err := s.db.QueryRow(`SELECT seedance_group_id, seedance_group_version FROM users WHERE id = ?`, userID).Scan(&gid, &ver)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrSeedanceGroupUserNotFound
	}
	if err != nil {
		return "", 0, err
	}
	return gid.String, int(ver.Int64), nil
}

func (s *SQLiteSeedanceGroupStore) SetSeedanceGroup(userID int64, groupID string, version int) error {
	_, err := s.db.Exec(`UPDATE users SET seedance_group_id = ?, seedance_group_version = ? WHERE id = ?`, groupID, version, userID)
	return err
}
