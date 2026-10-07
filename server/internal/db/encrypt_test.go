package db_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3/driver"

	"github.com/james/huabu_studio/apps/api/internal/db"
)

var plainHeader = []byte("SQLite format 3\x00")

func readHead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < len(plainHeader) {
		t.Fatalf("%s 太短：%d 字节", p, len(b))
	}
	return b
}

// 新建的库是密文：没有 SQLite 文件头，明文内容搜不到，不带密钥打不开。
func TestOpenCreatesEncryptedDatabase(t *testing.T) {
	p := filepath.Join(t.TempDir(), "data.db")
	conn, err := db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO settings (key, value) VALUES ('upstream_api_key', 'sk-very-secret-key')`); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	b := readHead(t, p)
	if bytes.HasPrefix(b, plainHeader) {
		t.Fatal("库文件仍是明文 SQLite 头")
	}
	if bytes.Contains(b, []byte("sk-very-secret-key")) || bytes.Contains(b, []byte("CREATE TABLE")) {
		t.Fatal("库文件里能直接搜到明文内容")
	}
	plain, err := driver.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	var n int
	if err := plain.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n); err == nil {
		t.Fatal("不带密钥也能读库")
	}
	if !db.IsDatabaseFile(p) {
		t.Fatal("IsDatabaseFile 应认出加密库")
	}
}

// 旧版明文库首次打开时原地转成加密库，数据不丢，不留中间文件。
func TestOpenEncryptsLegacyPlainDatabase(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.db")
	plain, err := driver.Open("file:" + p + "?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Exec(`CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT); INSERT INTO kv VALUES ('a', 'legacy-value')`); err != nil {
		t.Fatal(err)
	}
	plain.Close()
	if !bytes.HasPrefix(readHead(t, p), plainHeader) {
		t.Fatal("前置条件：应先得到明文库")
	}

	conn, err := db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	var v string
	if err := conn.QueryRow(`SELECT v FROM kv WHERE k = 'a'`).Scan(&v); err != nil || v != "legacy-value" {
		t.Fatalf("转换后读不到旧数据：v=%q err=%v", v, err)
	}
	conn.Close()

	b := readHead(t, p)
	if bytes.HasPrefix(b, plainHeader) || bytes.Contains(b, []byte("legacy-value")) {
		t.Fatal("旧库没有被加密")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "encrypting") {
			t.Fatalf("残留中间文件 %s", e.Name())
		}
	}
	// 再次打开（已是加密库）不应重复转换或出错。
	conn, err = db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

// VACUUM INTO 到 EncryptedURI 得到的副本也是密文（数据目录迁移走这条路）。
func TestVacuumIntoEncryptedURIStaysEncrypted(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy dir", "copy.db")
	os.MkdirAll(filepath.Dir(dst), 0o700)
	uri, err := db.EncryptedURI(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`VACUUM INTO ?`, uri); err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(readHead(t, dst), plainHeader) {
		t.Fatal("副本是明文")
	}
	if !db.IsDatabaseFile(dst) {
		t.Fatal("副本应能用本应用密钥打开")
	}
}

func TestIsDatabaseFileRejectsGarbage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "data.db")
	os.WriteFile(p, bytes.Repeat([]byte{0x5a}, 8192), 0o600)
	if db.IsDatabaseFile(p) {
		t.Fatal("随机内容不应被认成数据库")
	}
	if db.IsDatabaseFile(filepath.Join(t.TempDir(), "missing.db")) {
		t.Fatal("不存在的文件不应被认成数据库")
	}
}

// 明文库转加密前留一份明文备份（0600、可直接用 sqlite 打开）；过期备份在下次打开时被清理。
func TestEncryptLegacyKeepsPlaintextBackup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.db")
	// 模拟崩溃留下的热 WAL：连接开着、关掉自动检查点时把库和 WAL 拷走。
	// 数据只在 WAL 里，备份前必须先检查点，否则备份缺数据。
	orig := filepath.Join(dir, "orig.db")
	plain, err := driver.Open("file:" + orig + "?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	plain.SetMaxOpenConns(1)
	if _, err := plain.Exec(`CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT); INSERT INTO kv VALUES ('a', 'in-wal-value')`); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal"} {
		b, err := os.ReadFile(orig + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p+suffix, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plain.Close()
	if st, err := os.Stat(p + "-wal"); err != nil || st.Size() == 0 {
		t.Fatal("前置条件：应有非空的热 WAL")
	}
	if !bytes.HasPrefix(readHead(t, p), plainHeader) {
		t.Fatal("前置条件：应先得到明文库")
	}

	conn, err := db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	backups, _ := filepath.Glob(p + ".plaintext-backup-*")
	if len(backups) != 1 {
		t.Fatalf("应有 1 份明文备份，实际 %v", backups)
	}
	st, err := os.Stat(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("备份权限 = %o，期望 600", st.Mode().Perm())
	}
	if !bytes.HasPrefix(readHead(t, backups[0]), plainHeader) {
		t.Fatal("备份应是明文 SQLite 库")
	}
	bk, err := driver.Open("file:" + backups[0] + "?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	var v string
	err = bk.QueryRow(`SELECT v FROM kv WHERE k = 'a'`).Scan(&v)
	bk.Close()
	if err != nil || v != "in-wal-value" {
		t.Fatalf("备份里读不到旧数据：v=%q err=%v", v, err)
	}

	// 再次打开：已加密，不再产生新备份；未过期的备份保留。
	conn, err = db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if again, _ := filepath.Glob(p + ".plaintext-backup-*"); len(again) != 1 {
		t.Fatalf("重复打开后备份数应仍为 1，实际 %v", again)
	}
}

func TestOpenRemovesExpiredPlaintextBackups(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.db")
	now := time.Now()
	expired := db.PlaintextBackupPath(p, now.Add(-8*24*time.Hour))
	fresh := db.PlaintextBackupPath(p, now.Add(-6*24*time.Hour))
	other := filepath.Join(dir, "other.db.plaintext-backup-20000101-000000")
	for _, f := range []string{expired, fresh, other} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 文件名里时间戳解析不了时按修改时间判断。
	odd := p + ".plaintext-backup-garbled"
	os.WriteFile(odd, []byte("x"), 0o600)
	old := now.Add(-30 * 24 * time.Hour)
	os.Chtimes(odd, old, old)

	conn, err := db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	for f, want := range map[string]bool{expired: false, odd: false, fresh: true, other: true} {
		_, err := os.Stat(f)
		if got := err == nil; got != want {
			t.Errorf("%s 存在=%v，期望 %v", filepath.Base(f), got, want)
		}
	}
}

// IsDatabaseFile 是只读探测：不在库旁边留下 -wal/-shm。
func TestIsDatabaseFileLeavesNoSidecarFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.db")
	conn, err := db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	before, _ := os.ReadDir(dir)
	if !db.IsDatabaseFile(p) {
		t.Fatal("应认出加密库")
	}
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Fatalf("探测前 %d 个文件，探测后 %d 个", len(before), len(after))
	}
}
