package db_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
