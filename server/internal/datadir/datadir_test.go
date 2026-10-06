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
	mustWrite(t, filepath.Join(empty, datadir.PointerFileName+".tmp"), "{")
	mustWrite(t, filepath.Join(empty, "System Volume Information", "x"), "x")
	mustWrite(t, filepath.Join(empty, "$RECYCLE.BIN", "x"), "x")
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
	if exists(filepath.Join(to, datadir.PointerFileName)) {
		t.Fatal("指针文件不应复制")
	}
	if b, _ := os.ReadFile(filepath.Join(to, "diag", "sample.mp4")); string(b) != "v" {
		t.Fatal("diag 应随数据一起复制，否则旧目录清理不掉、无法迁回")
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

func newSourceDB(t *testing.T, dir string) {
	t.Helper()
	conn, err := db.Open(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
}

type event struct{ op, path string }

func indexOf(evs []event, op, path string) int {
	for i, e := range evs {
		if e.op == op && e.path == path {
			return i
		}
	}
	return -1
}

func lastIndexOf(evs []event, op, path string) int {
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].op == op && evs[i].path == path {
			return i
		}
	}
	return -1
}

// I2：新副本的文件、目录项、数据库都要先 fsync，quick_check 在 fsync 之后，最后才去掉标记。
func TestMigrateDurabilityOrder(t *testing.T) {
	from, base := t.TempDir(), t.TempDir()
	newSourceDB(t, from)
	mustWrite(t, filepath.Join(from, "media", "u1", "a.png"), "PNG")
	to := filepath.Join(base, "x", "dest")
	var evs []event
	defer datadir.SetTrace(func(op, p string) { evs = append(evs, event{op, p}) })()
	if err := datadir.Migrate(from, to); err != nil {
		t.Fatal(err)
	}
	must := func(op, p string) int {
		t.Helper()
		i := indexOf(evs, op, p)
		if i < 0 {
			t.Fatalf("缺少 %s %s；事件：%v", op, p, evs)
		}
		return i
	}
	before := func(a, b int, what string) {
		t.Helper()
		if a >= b {
			t.Fatalf("顺序错误：%s；事件：%v", what, evs)
		}
	}
	// 新建目录：父目录要 fsync
	must("fsyncdir", base)
	must("fsyncdir", filepath.Join(base, "x"))
	// 媒体文件 fsync → 所在目录 fsync（自底向上）
	png := must("fsync", filepath.Join(to, "media", "u1", "a.png"))
	u1 := must("fsyncdir", filepath.Join(to, "media", "u1"))
	media := must("fsyncdir", filepath.Join(to, "media"))
	before(png, u1, "文件先于目录")
	before(u1, media, "子目录先于父目录")
	// 数据库：VACUUM 输出 fsync → rename → 父目录 fsync → quick_check
	dbf := must("fsync", filepath.Join(to, "data.db.migrating"))
	qc := must("quick_check", filepath.Join(to, "data.db"))
	before(dbf, qc, "数据库 fsync 先于 quick_check")
	dirAfterRename := -1
	for i := dbf + 1; i < qc; i++ {
		if evs[i] == (event{"fsyncdir", to}) {
			dirAfterRename = i
		}
	}
	if dirAfterRename < 0 {
		t.Fatalf("rename 后、quick_check 前应 fsync 目标目录；事件：%v", evs)
	}
	// 全部落盘之后才去掉标记，去掉后再 fsync 目录
	unmark := must("unmark", to)
	before(media, unmark, "媒体落盘先于去标记")
	before(qc, unmark, "quick_check 先于去标记")
	if last := lastIndexOf(evs, "fsyncdir", to); last != len(evs)-1 || last < unmark {
		t.Fatalf("去标记后应以 fsync 目标目录收尾；事件：%v", evs)
	}
}

// I2：Cleanup 在删除任何旧数据之前先把新目录落盘。
func TestCleanupSyncsNewDirBeforeRemoving(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	for _, p := range []string{"data.db", "media/a.png"} {
		mustWrite(t, filepath.Join(oldDir, p), "x")
		mustWrite(t, filepath.Join(newDir, p), "x")
	}
	var evs []event
	defer datadir.SetTrace(func(op, p string) { evs = append(evs, event{op, p}) })()
	if err := datadir.Cleanup(oldDir, newDir); err != nil {
		t.Fatal(err)
	}
	syncNew := indexOf(evs, "fsyncdir", newDir)
	syncMedia := indexOf(evs, "fsyncdir", filepath.Join(newDir, "media"))
	firstRemove := -1
	for i, e := range evs {
		if e.op == "remove" {
			firstRemove = i
			break
		}
	}
	if syncNew < 0 || syncMedia < 0 || firstRemove < 0 || syncNew > firstRemove || syncMedia > firstRemove {
		t.Fatalf("应先 fsync 新目录再删旧数据；事件：%v", evs)
	}
}

func TestCleanupRefusesWhileMigrationMarkerPresent(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(oldDir, "data.db"), "x")
	mustWrite(t, filepath.Join(newDir, "data.db"), "x")
	mustWrite(t, filepath.Join(newDir, ".miaoju-migrating"), "migrating")
	if err := datadir.Cleanup(oldDir, newDir); err == nil || !exists(filepath.Join(oldDir, "data.db")) {
		t.Fatalf("新目录还有迁移标记时必须拒绝清理: %v", err)
	}
}

// M4：新旧目录互相包含时拒绝清理。
func TestCleanupRefusesNestedDirs(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "inner")
	mustWrite(t, filepath.Join(inner, "data.db"), "x")
	mustWrite(t, filepath.Join(outer, "data.db"), "x")
	if err := datadir.Cleanup(outer, inner); err == nil || !strings.Contains(err.Error(), "互相包含") {
		t.Fatalf("旧目录包含新目录应拒绝: %v", err)
	}
	if err := datadir.Cleanup(inner, outer); err == nil || !strings.Contains(err.Error(), "互相包含") {
		t.Fatalf("新目录包含旧目录应拒绝: %v", err)
	}
	if !exists(filepath.Join(outer, "data.db")) || !exists(filepath.Join(inner, "data.db")) {
		t.Fatal("拒绝时不得删除任何内容")
	}
}

// I3：默认目录有 diag 时，迁出 → 清理 → 还能迁回默认目录。
func TestDiagDoesNotBlockMigratingBack(t *testing.T) {
	def, d1 := t.TempDir(), filepath.Join(t.TempDir(), "d1")
	newSourceDB(t, def)
	mustWrite(t, filepath.Join(def, "diag", "sample.mp4"), "v")
	mustWrite(t, filepath.Join(def, datadir.PointerFileName), "{}")
	if err := datadir.Migrate(def, d1); err != nil {
		t.Fatal(err)
	}
	if err := datadir.Cleanup(def, d1); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(def, "diag")) {
		t.Fatal("diag 应随清理删除")
	}
	if _, err := datadir.ValidateTarget(d1, def); err != nil {
		t.Fatalf("应能迁回默认目录: %v", err)
	}
	if err := datadir.Migrate(d1, def); err != nil {
		t.Fatalf("迁回默认目录失败: %v", err)
	}
	if !exists(filepath.Join(def, "diag", "sample.mp4")) {
		t.Fatal("迁回后 diag 应在默认目录")
	}
}

// 整合：P8 壳的 location.json 与 P4 的待办文件一样放在默认数据目录里，既不能复制过去，
// 也不能被 Cleanup 当成“新目录也有”的条目删掉（删掉就回到默认目录了）。
func TestShellLocationFileIsNeverCopiedOrDeleted(t *testing.T) {
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "dest")
	mustWrite(t, filepath.Join(from, "media", "a.png"), "PNG")
	mustWrite(t, filepath.Join(from, datadir.LocationFileName), `{"version":1,"dataDir":"/x"}`)
	mustWrite(t, filepath.Join(from, datadir.LocationFileName+".tmp"), `{`)
	if err := datadir.Migrate(from, to); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(to, datadir.LocationFileName)) || exists(filepath.Join(to, datadir.LocationFileName+".tmp")) {
		t.Fatal("location.json 不应复制")
	}
	mustWrite(t, filepath.Join(to, datadir.LocationFileName), "{}") // 即便新目录里恰好也有
	mustWrite(t, filepath.Join(to, "data.db"), "x")
	if err := datadir.Cleanup(from, to); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(from, datadir.LocationFileName)) {
		t.Fatal("Cleanup 不得删除 location.json")
	}
	if _, err := datadir.ValidateTarget(to, from); err != nil {
		t.Fatalf("只剩 location.json 的默认目录应可再次选中: %v", err)
	}
}

func TestValidateChosenFolderUsesDedicatedSubdir(t *testing.T) {
	cur := t.TempDir()
	picked := t.TempDir()
	mustWrite(t, filepath.Join(picked, "别的文件.txt"), "x")
	got, err := datadir.InspectChosenFolder(cur, picked)
	if err != nil || got.Path != filepath.Join(picked, datadir.DedicatedSubdir) {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := datadir.InspectChosenFolder(cur, filepath.Join(picked, "不存在")); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("所选文件夹不存在应报错: %v", err)
	}
	if _, err := datadir.InspectChosenFolder(cur, "  "); err == nil {
		t.Fatal("空路径应报错")
	}
}
