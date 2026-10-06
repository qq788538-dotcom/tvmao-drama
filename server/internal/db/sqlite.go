// Package db 负责打开桌面端的 SQLite 数据库并执行嵌入式迁移。
//
// 与参考仓（MySQL）的差异集中在这里：
//   - 事务锁模式 _txlock=immediate：Begin 即拿写锁，替代 SELECT … FOR UPDATE；
//     后台 worker 与 HTTP 写请求争锁时排队等待（busy_timeout），而不是在锁升级处报 SQLITE_BUSY。
//   - WAL：读不阻塞写。
//   - 外键默认关闭，必须显式打开；workflow_snapshots 的级联删除依赖它。
package db

import (
	"context"
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
//
// 路径不能含 "?"，也不能以 "file:" 开头：驱动按 DSN 解析，会在第一个 "?" 处切开，
// 把后半截当参数，库被静默建到别处且所有 pragma 失效；"file:" 前缀同理。这里提前明确报错。
func Open(path string) (*sql.DB, error) {
	if strings.Contains(path, "?") || strings.HasPrefix(path, "file:") {
		return nil, fmt.Errorf("数据库路径不能包含 \"?\"，也不能以 \"file:\" 开头：%s", path)
	}
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
//
// 迁移在外键关闭的专用连接上执行（PRAGMA foreign_keys=OFF → BEGIN → 执行 → foreign_key_check
// → 提交 → 恢复 ON）。原因：SQLite 不能 ALTER COLUMN，改列要「建新表、拷数据、DROP 旧表、改名」，
// 外键开着时 DROP 旧表会触发 ON DELETE CASCADE，悄悄删光子表行（如重建 projects 会清空所有快照）；
// 而 PRAGMA foreign_keys 在事务内是空操作，所以迁移文件里写 PRAGMA 不起作用，必须由这里在事务外切换。
// 提交前若 foreign_key_check 发现悬挂引用，整个迁移回滚并报错。
func Migrate(conn *sql.DB) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	return migrate(conn, sub)
}

// migrate 对 files 根目录下的 *.sql 按文件名顺序执行尚未执行的迁移（拆出来供测试注入迁移集）。
func migrate(conn *sql.DB, files fs.FS) error {
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

	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		if applied[name] {
			continue
		}
		raw, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		if err := applyMigration(conn, name, string(raw)); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(conn *sql.DB, name, script string) (err error) {
	ctx := context.Background()
	c, err := conn.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("迁移 %s 关闭外键: %w", name, err)
	}
	// 无论成败都要把外键开回去再还连接池；开不回去就报错，免得带着外键关闭的连接继续服务。
	defer func() {
		if _, e := c.ExecContext(ctx, `PRAGMA foreign_keys=ON`); e != nil && err == nil {
			err = fmt.Errorf("迁移 %s 恢复外键: %w", name, e)
		}
	}()

	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, script); err != nil {
		tx.Rollback()
		return fmt.Errorf("执行迁移 %s: %w", name, err)
	}
	// 外键关闭期间迁移可能留下悬挂引用，提交前整库检查一遍。
	fkRows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("迁移 %s 外键检查: %w", name, err)
	}
	if fkRows.Next() {
		var table string
		var rowid sql.NullInt64
		var parent string
		var fkid int64
		_ = fkRows.Scan(&table, &rowid, &parent, &fkid)
		fkRows.Close()
		tx.Rollback()
		return fmt.Errorf("迁移 %s 留下悬挂外键：表 %s 引用了不存在的 %s", name, table, parent)
	}
	if err := fkRows.Err(); err != nil {
		fkRows.Close()
		tx.Rollback()
		return err
	}
	fkRows.Close()
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
		tx.Rollback()
		return fmt.Errorf("记录迁移 %s: %w", name, err)
	}
	return tx.Commit()
}
