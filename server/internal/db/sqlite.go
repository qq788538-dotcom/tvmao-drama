// Package db 负责打开桌面端的 SQLite 数据库并执行嵌入式迁移。
//
// 与参考仓（MySQL）的差异集中在这里：
//   - 事务锁模式 _txlock=immediate：Begin 即拿写锁，替代 SELECT … FOR UPDATE；
//     后台 worker 与 HTTP 写请求争锁时排队等待（busy_timeout），而不是在锁升级处报 SQLITE_BUSY。
//   - WAL：读不阻塞写。
//   - 外键默认关闭，必须显式打开；workflow_snapshots 的级联删除依赖它。
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// TimeLayout 是库里时间列的统一文本格式，与 SQLite 的 CURRENT_TIMESTAMP 一致（UTC，秒级）。
const TimeLayout = "2006-01-02 15:04:05"

// Time 把 Go 时间转成库里的统一格式。SQL 参数里传时间一律经它，才能与列值直接比较。
func Time(t time.Time) string { return t.UTC().Format(TimeLayout) }

// Open 打开（不存在则创建）path 处的数据库。path 是普通文件路径，可以含空格。
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建数据库目录: %w", err)
	}
	q := url.Values{}
	for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(10000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	conn, err := sql.Open("sqlite", path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	// WAL 下多个读连接可并发；写由 immediate 事务 + busy_timeout 串行化。
	conn.SetMaxOpenConns(8)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	return conn, nil
}

// Migrate 按文件名顺序执行尚未执行过的嵌入迁移；每个文件一个事务，失败整体回滚。
func Migrate(conn *sql.DB) error {
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("创建 schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := conn.Query(`SELECT name FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		applied[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	paths, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, p := range paths {
		name := strings.TrimPrefix(p, "migrations/")
		if applied[name] {
			continue
		}
		raw, err := migrationsFS.ReadFile(p)
		if err != nil {
			return err
		}
		if err := applyMigration(conn, name, string(raw)); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(conn *sql.DB, name, script string) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(script); err != nil {
		tx.Rollback()
		return fmt.Errorf("执行迁移 %s: %w", name, err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
		tx.Rollback()
		return fmt.Errorf("记录迁移 %s: %w", name, err)
	}
	return tx.Commit()
}
