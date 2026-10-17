package users

import (
	"errors"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

func TestGetByIDReturnsLocalAdmin(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	u, err := repo.GetByID(1)
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "admin" || u.Email != "local@miaoju.desktop" || u.StorageQuotaBytes != 1125899906842624 || u.CreatedAt.IsZero() {
		t.Fatalf("u=%+v", u)
	}
	if _, err := repo.GetByID(2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestAddStorageAndUsage(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	if err := repo.AddStorage(1, 100); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddStorage(1, -30); err != nil {
		t.Fatal(err)
	}
	tx, _ := conn.Begin()
	if err := repo.AddStorageTx(tx, 1, 5); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	used, quota, err := repo.StorageUsage(1)
	if err != nil || used != 75 || quota != 1125899906842624 {
		t.Fatalf("used=%d quota=%d err=%v", used, quota, err)
	}
	if _, _, err := repo.StorageUsage(99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

// /me 展示的用量从 assets + library_items 实时算，不信增量计数器（会漂移）。
func TestComputeStorageUsedSumsAssetsAndLibrary(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	conn.Exec(`INSERT INTO assets (user_id, object_key, url, size_bytes, mime_type) VALUES (1, 'a', 'media://a', 100, 'image/png'), (1, 'b', 'media://b', 0, 'image/png')`)
	res, _ := conn.Exec(`INSERT INTO library_folders (user_id, name) VALUES (1, 'f')`)
	fid, _ := res.LastInsertId()
	conn.Exec(`INSERT INTO library_items (user_id, folder_id, object_key, url, size_bytes, mime_type, kind) VALUES (1, ?, 'c', 'media://c', 23, 'image/png', 'image')`, fid)
	got, err := repo.ComputeStorageUsed(1)
	if err != nil || got != 123 {
		t.Fatalf("got=%d err=%v", got, err)
	}
	if got, _ := repo.ComputeStorageUsed(5); got != 0 {
		t.Fatalf("无数据的用户应为 0，got %d", got)
	}
}
