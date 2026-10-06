// Package dbtest 给各包的仓库测试提供一个真实的、已迁移的 SQLite 库。
package dbtest

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db"
)

// Open 在临时目录建一个新库并执行全部迁移，测试结束自动关闭。
// 文件名带空格，顺带覆盖 macOS「Application Support」这类路径。
func Open(t testing.TB) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test data.db"))
	if err != nil {
		t.Fatalf("dbtest: open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("dbtest: migrate: %v", err)
	}
	return conn
}
