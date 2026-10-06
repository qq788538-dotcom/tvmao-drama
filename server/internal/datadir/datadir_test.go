package datadir_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/datadir"
	"github.com/james/huabu_studio/apps/api/internal/db"
)

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestValidateTargetAcceptsNewOrEmptyDir(t *testing.T) {
	cur := t.TempDir()
	if got, err := datadir.ValidateTarget(cur, filepath.Join(t.TempDir(), "新目录", "子目录")+string(filepath.Separator)); err != nil || strings.HasSuffix(got, string(filepath.Separator)) {
		t.Fatalf("不存在的目录应通过并规范化: %q %v", got, err)
	}
	empty := t.TempDir()
	mustWrite(t, filepath.Join(empty, ".DS_Store"), "x")
	mustWrite(t, filepath.Join(empty, datadir.PointerFileName), "{}")
	if _, err := datadir.ValidateTarget(cur, empty); err != nil {
		t.Fatalf("只有可忽略文件的目录应通过: %v", err)
	}
}

func TestValidateTargetRejects(t *testing.T) {
	cur := t.TempDir()
	nonEmpty := t.TempDir()
	mustWrite(t, filepath.Join(nonEmpty, "photo.jpg"), "x")
	file := filepath.Join(t.TempDir(), "a.txt")
	mustWrite(t, file, "x")
	resolved, _ := filepath.EvalSymlinks(cur) // macOS 上 TempDir 在 /var → /private/var 的符号链接下
	cases := map[string]string{
		"":                             "请选择",
		"/tmp/a?b":                     `"?"`,
		"file:/tmp/x":                  `"file:"`,
		"relative/dir":                 "绝对路径",
		cur:                            "相同",
		filepath.Join(cur, "sub"):      "里面",
		filepath.Join(resolved, "sub"): "里面",
		filepath.Dir(cur):              "上级",
		nonEmpty:                       "不是空的",
		file:                           "不是文件夹",
		filepath.Join(file, "child"):   "同名文件",
	}
	for target, want := range cases {
		_, err := datadir.ValidateTarget(cur, target)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err=%v want 包含 %q", target, err, want)
		}
	}
}

// 源库开着连接、关闭自动 checkpoint，刚写入的行只在 -wal 里；只拷 data.db 会丢它。
func TestMigrateCopiesDBIncludingUncheckpointedWAL(t *testing.T) {
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "dest")
	conn, err := db.Open(filepath.Join(from, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO settings ("key", value) VALUES ('probe', 'in-wal')`); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(from, "data.db-wal")); err != nil || st.Size() == 0 {
		t.Fatalf("前置条件：WAL 应非空: %v", err)
	}
	if err := datadir.Migrate(from, to); err != nil {
		t.Fatal(err)
	}
	dst, err := db.Open(filepath.Join(to, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	var v string
	if err := dst.QueryRow(`SELECT value FROM settings WHERE "key" = 'probe'`).Scan(&v); err != nil || v != "in-wal" {
		t.Fatalf("新库缺少 WAL 中的数据: %q %v", v, err)
	}
	if exists(filepath.Join(to, ".miaoju-migrating")) || exists(filepath.Join(to, "data.db.migrating")) {
		t.Fatal("成功后不应残留标记或临时文件")
	}
}

func TestMigrateCopiesTreeAndSkipsPointer(t *testing.T) {
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "dest")
	mustWrite(t, filepath.Join(from, "media", "u1", "2026", "10", "a.png"), "PNG")
	mustWrite(t, filepath.Join(from, "config", "x.json"), "{}")
	mustWrite(t, filepath.Join(from, datadir.PointerFileName), `{"dataDir":"/old"}`)
	mustWrite(t, filepath.Join(from, "diag", "sample.mp4"), "v")
	if err := datadir.Migrate(from, to); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(to, "media", "u1", "2026", "10", "a.png")); string(b) != "PNG" {
		t.Fatal("媒体未复制")
	}
	if !exists(filepath.Join(to, "config", "x.json")) {
		t.Fatal("config 未复制")
	}
	if exists(filepath.Join(to, datadir.PointerFileName)) || exists(filepath.Join(to, "diag")) {
		t.Fatal("指针文件和诊断样本不应复制")
	}
	if !exists(filepath.Join(from, "media", "u1", "2026", "10", "a.png")) {
		t.Fatal("Migrate 不得删除源数据")
	}
}

func TestMigrateResumesAfterPartialTarget(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(from, "media", "a.png"), "PNG")
	// 模拟上次迁移中途断电：标记还在，留了半截文件
	mustWrite(t, filepath.Join(to, ".miaoju-migrating"), "migrating")
	mustWrite(t, filepath.Join(to, "media", "half.png"), "HA")
	if err := datadir.Migrate(from, to); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(to, "media", "half.png")) || !exists(filepath.Join(to, "media", "a.png")) {
		t.Fatal("应清空半成品后重新复制")
	}
}

func TestMigrateFailureLeavesSourceIntact(t *testing.T) {
	from := t.TempDir()
	mustWrite(t, filepath.Join(from, "media", "a.png"), "PNG")
	blocker := filepath.Join(t.TempDir(), "blocker")
	mustWrite(t, blocker, "file")
	if err := datadir.Migrate(from, filepath.Join(blocker, "dest")); err == nil {
		t.Fatal("目标父路径是文件，应失败")
	}
	if !exists(filepath.Join(from, "media", "a.png")) {
		t.Fatal("失败时源数据必须完好")
	}
}

func TestCleanupRemovesOnlyCopiedEntries(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	for _, p := range []string{"data.db", "data.db-wal", "media/a.png", "config/x.json", "keepme.txt", datadir.PointerFileName} {
		mustWrite(t, filepath.Join(oldDir, p), "x")
	}
	for _, p := range []string{"data.db", "media/a.png", "config/x.json"} {
		mustWrite(t, filepath.Join(newDir, p), "x")
	}
	if err := datadir.Cleanup(oldDir, newDir); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"data.db", "data.db-wal", "media", "config"} {
		if exists(filepath.Join(oldDir, gone)) {
			t.Errorf("%s 应已删除", gone)
		}
	}
	for _, kept := range []string{"keepme.txt", datadir.PointerFileName} {
		if !exists(filepath.Join(oldDir, kept)) {
			t.Errorf("%s 不应删除", kept)
		}
	}
	// 清理后旧目录又可以作为迁移目标（只剩指针文件时）
	os.Remove(filepath.Join(oldDir, "keepme.txt"))
	if _, err := datadir.ValidateTarget(newDir, oldDir); err != nil {
		t.Fatalf("清理后的默认目录应可再次选中: %v", err)
	}
}

func TestCleanupRefusesWithoutNewDB(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(oldDir, "data.db"), "x")
	if err := datadir.Cleanup(oldDir, newDir); err == nil || !exists(filepath.Join(oldDir, "data.db")) {
		t.Fatalf("新目录没有 data.db 时必须拒绝清理: %v", err)
	}
	if err := datadir.Cleanup(oldDir, oldDir); err == nil {
		t.Fatal("新旧相同应拒绝")
	}
}
