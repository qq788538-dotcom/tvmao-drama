package agent

import (
	"database/sql"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

// openTestDB 给仓库测试一个全新的、已迁移的 SQLite 库。
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return dbtest.Open(t)
}
