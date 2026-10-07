// Package db 负责打开桌面端的 SQLite 数据库并执行嵌入式迁移。
//
// 与参考仓（MySQL）的差异集中在这里：
//   - 事务锁模式 _txlock=immediate：Begin 即拿写锁，替代 SELECT … FOR UPDATE；
//     后台 worker 与 HTTP 写请求争锁时排队等待（busy_timeout），而不是在锁升级处报 SQLITE_BUSY。
//   - WAL：读不阻塞写。
//   - 外键默认关闭，必须显式打开；workflow_snapshots 的级联删除依赖它。
//   - 整库加密（adiantum VFS）：库文件、WAL、回滚日志都是密文，拷走 data.db 用 sqlite 打不开。
//     密钥在二进制里，只防「直接打开 sqlite 看配置」，挡不住逆向——这是有意的取舍。
package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/vfs/adiantum"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// TimeLayout 是库里时间列的统一文本格式，与 SQLite 的 CURRENT_TIMESTAMP 一致（UTC，秒级）。
const TimeLayout = "2006-01-02 15:04:05"

// Time 把 Go 时间转成库里的统一格式。SQL 参数里传时间一律经它，才能与列值直接比较。
func Time(t time.Time) string { return t.UTC().Format(TimeLayout) }

// encryptionKeyHex 是库的 256 位密钥（hex）。运行时由种子串派生，二进制里不出现密钥本身；
// 改种子串等于换密钥，老库会打不开——不要改。
var encryptionKeyHex = func() string {
	sum := sha256.Sum256([]byte("miaoju-desktop\x00data.db\x00adiantum\x00v1\x00b6f1c0e2"))
	return hex.EncodeToString(sum[:])
}()

// plainHeader 是未加密 SQLite 文件的文件头；加密库的头是密文，不会以它开头。
var plainHeader = []byte("SQLite format 3\x00")

// Open 打开（不存在则创建）path 处的加密数据库。path 是普通文件路径，可以含空格。
// path 处若是旧版的明文库，先原地转成加密库（encryptLegacy）。
//
// 路径不能含 "?"，也不能以 "file:" 开头：历史上驱动按 DSN 解析会在第一个 "?" 处切开；
// 现在虽然经 fileURI 转义，限制保留，免得数据目录迁移等调用方各自放宽出分叉。
func Open(path string) (*sql.DB, error) {
	if strings.Contains(path, "?") || strings.HasPrefix(path, "file:") {
		return nil, fmt.Errorf("数据库路径不能包含 \"?\"，也不能以 \"file:\" 开头：%s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建数据库目录: %w", err)
	}
	plain, err := isPlainDatabase(path)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	if plain {
		if err := encryptLegacy(path); err != nil {
			return nil, fmt.Errorf("加密旧数据库: %w", err)
		}
	}
	dsn, err := EncryptedURI(path)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(10000)", "synchronous(NORMAL)", "temp_store(memory)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	// sqlite：time.Time 参数按 CURRENT_TIMESTAMP 同款格式写入（业务代码本就统一经 db.Time 传字符串）。
	q.Set("_timefmt", "sqlite")
	conn, err := driver.Open(dsn + "&" + q.Encode())
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

// EncryptedURI 返回 path 处加密库的 SQLite URI（带 vfs 与密钥参数，可再追加 &参数）。
// VACUUM INTO 的目标也必须用它，否则导出的副本是明文。
func EncryptedURI(path string) (string, error) {
	u, err := fileURI(path)
	if err != nil {
		return "", err
	}
	return u + "?vfs=adiantum&hexkey=" + encryptionKeyHex, nil
}

// fileURI 把普通路径转成 file: URI：空格等字符转义；Windows 盘符路径变成 file:///C:/...。
func fileURI(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String(), nil
}

// IsDatabaseFile 判断 path 是否为本应用能打开的库：旧版明文库，或用本应用密钥加密的库。只读探测，不改文件。
func IsDatabaseFile(path string) bool {
	if plain, err := isPlainDatabase(path); err != nil {
		return false
	} else if plain {
		return true
	}
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return false
	}
	dsn, err := EncryptedURI(path)
	if err != nil {
		return false
	}
	conn, err := driver.Open(dsn + "&mode=ro")
	if err != nil {
		return false
	}
	defer conn.Close()
	var n int
	return conn.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n) == nil
}

// isPlainDatabase 判断 path 是否为未加密的 SQLite 库。文件不存在或为空返回 false。
func isPlainDatabase(path string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	head := make([]byte, len(plainHeader))
	if _, err := io.ReadFull(f, head); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		return false, err
	}
	return bytes.Equal(head, plainHeader), nil
}

// encryptLegacy 把 path 处的明文库原地换成加密库：明文库做一次 TRUNCATE 检查点把 WAL 并回主库，
// VACUUM INTO 写出加密副本，关闭后改名覆盖。改名前任何一步失败，明文库原样保留、下次启动重试；
// 改名是原子的，不会出现半明半密的库。
func encryptLegacy(path string) error {
	tmp := path + ".encrypting"
	_ = os.Remove(tmp)
	src, err := fileURI(path)
	if err != nil {
		return err
	}
	dst, err := EncryptedURI(tmp)
	if err != nil {
		return err
	}
	plain, err := driver.Open(src + "?_pragma=busy_timeout(10000)")
	if err != nil {
		return err
	}
	_, err = plain.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if err == nil {
		_, err = plain.Exec(`VACUUM INTO ?`, dst)
	}
	if cerr := plain.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if f, err := os.Open(tmp); err == nil {
		err = f.Sync()
		f.Close()
		if err != nil {
			return err
		}
	}
	// 明文库的 WAL/SHM 已在检查点和关闭时并回主库；残留的也只会是明文，删掉。
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(tmp, path)
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
