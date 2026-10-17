// Package db 负责打开桌面端的 SQLite 数据库并执行嵌入式迁移。
//
// SQLite 特有的处理集中在这里：
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
	"runtime"
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
// path 处若是旧版的明文库，先备份明文库，再原地转成加密库（encryptLegacy）。
// 每次打开顺带清理过期（plaintextBackupTTL）的明文备份。
//
// 路径不能含 "?"，也不能以 "file:" 开头：历史上驱动按 DSN 解析会在第一个 "?" 处切开；
// 现在虽然经 fileURI 转义，限制保留，免得数据目录迁移等调用方各自放宽出分叉。
//
// 不要把 time.Time 直接作为 SQL 参数：驱动按 _timefmt=sqlite 写成秒级 UTC 文本，亚秒部分会被
// 悄悄截掉（微秒精度的列就错了）。时间一律先经 Time 或调用方自己的定宽格式转成字符串。
func Open(path string) (*sql.DB, error) {
	if strings.Contains(path, "?") || strings.HasPrefix(path, "file:") {
		return nil, fmt.Errorf("数据库路径不能包含 \"?\"，也不能以 \"file:\" 开头：%s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建数据库目录: %w", err)
	}
	cleanupPlaintextBackups(path, time.Now())
	plain, err := isPlainDatabase(path)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	if plain {
		if err := encryptLegacy(path, time.Now()); err != nil {
			return nil, fmt.Errorf("加密旧数据库: %w", err)
		}
	}
	dsn, err := EncryptedURI(path)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	// busy_timeout 必须排第一：后面的 journal_mode(WAL) 要拿锁，碰上别的连接在恢复或检查点时
	// 没有忙等会立刻 BUSY，新连接建不起来。
	for _, p := range []string{"busy_timeout(10000)", "foreign_keys(1)", "journal_mode(WAL)", "synchronous(NORMAL)", "temp_store(memory)"} {
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

// fileURI 把普通路径（相对路径先转绝对）转成本平台的 SQLite file: URI。
func fileURI(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return pathToURI(abs, runtime.GOOS), nil
}

// pathToURI 把 goos 平台上的绝对路径 abs 转成 SQLite 的 file: URI（纯函数，便于在任意平台测各平台）。
//
//   - Unix：file:///abs/path。
//   - Windows 盘符路径：file:C:/dir/data.db。不能写成 file:///C:/...——SQLite 会把 "/C:/..." 原样交给
//     VFS（去掉前导 "/" 的逻辑只在原生 os_win.c 里，WASM 版没有），Go 的 filepath.Abs 不认，报 CANTOPEN。
//     这也是 ncruces 在 Windows CI 上的写法（"file:" + filepath.ToSlash(abs)）。
//   - Windows UNC 路径 \\server\share\...：file:////server/share/...。"file://" 后面是空 authority，
//     路径本身以 "//" 开头；直接写 file://server/... 会被 SQLite 当成 authority 拒绝。
//
// 除字母、数字、"-._~/" 和盘符冒号外一律按 UTF-8 字节做 %XX 转义（"?"、"#"、"%"、空格、中文等），
// SQLite 解析 URI 时会逐个还原。
func pathToURI(abs, goos string) string {
	p := abs
	if goos == "windows" {
		p = strings.ReplaceAll(p, `\`, "/")
		if len(p) >= 2 && p[1] == ':' && isASCIILetter(p[0]) {
			// 盘符的冒号保留原样，其余照常转义。
			return "file:" + p[:2] + escapeURIPath(p[2:])
		}
		if strings.HasPrefix(p, "//") {
			return "file://" + escapeURIPath(p)
		}
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + escapeURIPath(p)
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func escapeURIPath(p string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if isASCIILetter(c) || c >= '0' && c <= '9' || strings.IndexByte("-._~/", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&0xf])
	}
	return b.String()
}

// IsDatabaseFile 判断 path 是否为本应用能打开的库：旧版明文库，或用本应用密钥加密的库。
// 只读探测，不改文件：以 immutable=1 打开，不建 -wal/-shm，只读介质上也能判断。
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
	conn, err := driver.Open(dsn + "&mode=ro&immutable=1")
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

// 明文备份：加密转换前把明文库拷成 <库名>.plaintext-backup-<时间>，供回退旧版本时手动恢复。
// 备份是明文，与「不让人直接打开库看到 Key」冲突，所以只留 plaintextBackupTTL，之后启动时删掉。
const (
	plaintextBackupInfix  = ".plaintext-backup-"
	plaintextBackupLayout = "20060102-150405"
	plaintextBackupTTL    = 7 * 24 * time.Hour
)

// PlaintextBackupPath 返回 path 处的库在 at 时刻的明文备份路径。
func PlaintextBackupPath(path string, at time.Time) string {
	return path + plaintextBackupInfix + at.UTC().Format(plaintextBackupLayout)
}

// cleanupPlaintextBackups 删除 path 同目录下早于 now-plaintextBackupTTL 的明文备份。
// 时间以文件名里的时间戳为准（解析不了再看修改时间）。尽力而为，失败不影响启动。
func cleanupPlaintextBackups(path string, now time.Time) {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := base + plaintextBackupInfix
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !e.Type().IsRegular() {
			continue
		}
		stamp := strings.TrimPrefix(name, prefix)
		// 有人用 sqlite 打开过备份的话会留下同名 -wal/-shm/-journal，跟着备份一起到期。
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			stamp = strings.TrimSuffix(stamp, suffix)
		}
		at, err := time.ParseInLocation(plaintextBackupLayout, stamp, time.UTC)
		if err != nil {
			info, ierr := e.Info()
			if ierr != nil {
				continue
			}
			at = info.ModTime()
		}
		if now.Sub(at) > plaintextBackupTTL {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// encryptLegacy 把 path 处的明文库原地换成加密库：
//
//  1. 明文库做 TRUNCATE 检查点，确认 WAL 已全部并回主库；
//  2. 把主库拷成明文备份（0600，fsync）；
//  3. VACUUM INTO 写出加密副本，fsync，quick_check 通过；
//  4. 删掉明文 WAL/SHM，改名覆盖，fsync 目录。
//
// 改名前任何一步失败，明文库原样保留、下次启动重试；改名是原子的，不会出现半明半密的库。
// 备份在成功后保留，由 cleanupPlaintextBackups 到期清理。
func encryptLegacy(path string, now time.Time) error {
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
	// 只用一条连接：检查点、拷贝、VACUUM INTO 期间不让池里冒出别的连接。
	plain.SetMaxOpenConns(1)
	backup := PlaintextBackupPath(path, now)
	backedUp := false
	err = checkpointTruncate(plain)
	if err == nil {
		err = copyFileSynced(path, backup)
		backedUp = err == nil
	}
	if err == nil {
		_, err = plain.Exec(`VACUUM INTO ?`, dst)
	}
	if cerr := plain.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = syncFile(tmp)
	}
	if err == nil {
		err = quickCheck(dst)
	}
	if err != nil {
		// 明文库原样还在，这次的备份多余；删掉，免得重试撞上同名备份。
		_ = os.Remove(tmp)
		if backedUp {
			_ = os.Remove(backup)
		}
		return err
	}
	// 检查点已确认 WAL 全部并回主库（且主库已备份）；残留的 WAL/SHM 只会是明文，删掉，
	// 免得加密库把它当自己的 WAL 读。
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

// checkpointTruncate 做 TRUNCATE 检查点，并确认没有因为别的读者而只做了一半。
func checkpointTruncate(conn *sql.DB) error {
	var busy, logFrames, checkpointed int
	if err := conn.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("检查点: %w", err)
	}
	if busy != 0 || logFrames != checkpointed {
		return fmt.Errorf("检查点未完成（busy=%d, log=%d, checkpointed=%d），可能有其他程序正打开数据库", busy, logFrames, checkpointed)
	}
	return nil
}

// quickCheck 用 uri 打开库跑一次 quick_check。
func quickCheck(uri string) error {
	conn, err := driver.Open(uri)
	if err != nil {
		return err
	}
	defer conn.Close()
	var res string
	if err := conn.QueryRow(`PRAGMA quick_check`).Scan(&res); err != nil {
		return fmt.Errorf("校验加密副本: %w", err)
	}
	if res != "ok" {
		return fmt.Errorf("加密副本校验失败：%s", res)
	}
	return nil
}

// copyFileSynced 把 from 拷到新文件 to（0600，已存在则报错），内容和目录项都落盘。
func copyFileSynced(from, to string) (err error) {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("创建明文备份: %w", err)
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(to)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return fmt.Errorf("写入明文备份: %w", err)
	}
	if err = out.Sync(); err != nil {
		return fmt.Errorf("写入明文备份: %w", err)
	}
	return syncDir(filepath.Dir(to))
}

func syncFile(p string) error {
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// syncDir fsync 目录本身，让新建 / 改名的目录项落盘。Windows 不支持对目录 fsync，跳过。
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
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
