// Package datadir 实现设置页的“修改数据目录”（spec S2）。
//
// 流程：设置页校验新目录（ValidateTarget）→ 壳记下待迁移路径并重启 → 下次启动时壳先调用
// `sidecar migrate-data-dir`（Migrate：复制 + 校验，不删源）→ 壳把指针文件改成新目录 →
// 再调用 `sidecar cleanup-data-dir`（Cleanup：删掉旧目录里已有副本的条目）→ 正常启动。
// 任一步中断，原目录都是完整的；新目录里的 .miaoju-migrating 标记让下次重做。
package datadir

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/james/huabu_studio/apps/api/internal/db"
)

// PointerFileName 是壳记录迁移待办（待迁移 / 待清理目录）的文件名，放在系统默认数据目录里，永不复制、永不删除。
const PointerFileName = "data-location.json"

// LocationFileName 是壳（P8 data_dir.rs）记录“当前用哪个数据目录”的文件，同样在默认数据目录里，
// 同样永不复制、永不删除：Cleanup 会删掉旧目录里“新目录也有”的条目，复制过去就会把它删掉。
const LocationFileName = "location.json"

// CLIEndpointFileName 是壳写在默认数据目录里的 CLI 地址文件（CLI spec 3.2）。和 location.json 一样
// 属于"默认目录的附属文件"：迁移不复制，清理不删除，也不让目录被判为非空。
const CLIEndpointFileName = "cli-endpoint.json"

const (
	markerName = ".miaoju-migrating"
	dbName     = "data.db"
	dbTmpName  = "data.db.migrating"
	// migratedFromName 记录“这份副本是从哪个目录迁来的”，在去掉迁移标记之前写入。下次启动重跑同一迁移时
	// （壳在迁移子进程跑完之前退出），据此认出“副本已完整”并视为成功（I2）。它不随后续迁移复制。
	migratedFromName = ".miaoju-migrated-from"
)


// ignorable 是判断“目录为空”时忽略的文件。
// 指针文件的临时文件（壳写指针时崩溃会留下）同样忽略。
// Windows 盘符根目录自带的 System Volume Information / $RECYCLE.BIN 也忽略，否则空 U 盘根目录会被判为非空。
var ignorable = map[string]bool{
	".DS_Store": true, "Thumbs.db": true, "desktop.ini": true,
	"System Volume Information": true, "$RECYCLE.BIN": true,
	PointerFileName: true, PointerFileName + ".tmp": true,
	LocationFileName: true, LocationFileName + ".tmp": true,
	CLIEndpointFileName: true, CLIEndpointFileName + ".tmp": true,
	MigrateLockFileName: true, InstanceLockFileName: true,
}

// notCopied 是复制时跳过的顶层条目：数据库单独用 VACUUM INTO 复制。
// diag（诊断样本）照常复制，这样 Cleanup 会把它从旧目录删掉，旧目录（通常是默认目录）才能再次被选为迁移目标。
var notCopied = map[string]bool{markerName: true, migratedFromName: true, dbName: true, dbName + "-wal": true, dbName + "-shm": true, dbTmpName: true}

// DedicatedSubdir 是用户所选文件夹下、由应用自建的专用数据目录名，与壳 datadir.rs 的 DEDICATED_SUBDIR 一致。
// 数据总放进专用子目录：用户可以直接选 ~/Documents 这类非空文件夹，壳也能放心把这个子目录收紧为 0700
// （不动用户所选文件夹本身的权限）。
const DedicatedSubdir = "MiaojuData"

// TargetState 是所选数据目录的状态（I1）：空（可迁移过去）/ 已有喵剧数据（可直接使用，不迁移）。
// 非空且不是喵剧数据目录的情况直接报错。
type TargetState string

const (
	TargetEmpty    TargetState = "empty"
	TargetExisting TargetState = "existing"
)

// Target 是设置页校验结果：实际数据目录路径与其状态。
type Target struct {
	Path  string
	State TargetState
}

// InspectChosenFolder 把用户在对话框里选的文件夹换成实际的数据目录并判断状态。
// 一般取其下的 MiaojuData 子目录；用户直接选中了一个已有数据的 MiaojuData 时就用它本身。
func InspectChosenFolder(current, picked string) (Target, error) {
	p := strings.TrimSpace(picked)
	if p == "" {
		return Target{}, errors.New("请选择新的数据目录")
	}
	if filepath.IsAbs(p) && !strings.Contains(p, "?") {
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			return Target{}, fmt.Errorf("所选文件夹不存在：%s", p)
		}
		if c := filepath.Clean(p); filepath.Base(c) == DedicatedSubdir && IsDataDir(c) {
			return InspectTarget(current, c)
		}
	}
	return InspectTarget(current, filepath.Join(p, DedicatedSubdir))
}

// IsDataDir 报告 dir 是否是一个可以直接使用的喵剧数据目录：有有效的 data.db，且没有未完成的迁移标记。
func IsDataDir(dir string) bool {
	if _, err := os.Lstat(filepath.Join(dir, markerName)); err == nil {
		return false
	}
	return isSQLiteFile(filepath.Join(dir, dbName))
}

func isSQLiteFile(p string) bool {
	return db.IsDatabaseFile(p)
}

// ValidateTarget 校验迁移目标，返回规范化后的路径。已有喵剧数据的目录不能作为迁移目标。错误文案直接展示给用户。
func ValidateTarget(current, target string) (string, error) {
	t, err := InspectTarget(current, target)
	if err != nil {
		return "", err
	}
	if t.State == TargetExisting {
		return "", errors.New("所选文件夹里已有喵剧数据，不能迁移到这里；可以在设置里选择它并“直接使用该目录”")
	}
	return t.Path, nil
}

// InspectTarget 校验 target 并判断状态（空 / 已有喵剧数据）；非空且无关时报错。
func InspectTarget(current, target string) (Target, error) {
	state := TargetEmpty
	t := strings.TrimSpace(target)
	if t == "" {
		return Target{}, errors.New("请选择新的数据目录")
	}
	// 与 db.Open 相同的限制：驱动按 DSN 解析路径，"?" 之后会被当成参数。
	if strings.Contains(t, "?") || strings.HasPrefix(t, "file:") {
		return Target{}, fmt.Errorf("数据目录路径不能包含 \"?\"，也不能以 \"file:\" 开头：%s", t)
	}
	if !filepath.IsAbs(t) {
		return Target{}, fmt.Errorf("请选择绝对路径：%s", t)
	}
	t = filepath.Clean(t)
	rc, rt := realPath(current), realPath(t)
	switch {
	case samePath(rc, rt):
		return Target{}, errors.New("新目录与当前数据目录相同")
	case within(rt, rc):
		return Target{}, errors.New("不能选择当前数据目录里面的文件夹")
	case within(rc, rt):
		return Target{}, errors.New("不能选择当前数据目录的上级文件夹")
	}
	st, err := os.Stat(t)
	switch {
	case err == nil && !st.IsDir():
		return Target{}, errors.New("所选路径不是文件夹")
	case err == nil:
		entries, err := os.ReadDir(t)
		if err != nil {
			return Target{}, fmt.Errorf("无法读取所选文件夹：%w", err)
		}
		switch {
		case hasEntry(entries, markerName):
			// 上次迁移没做完：迁移会清掉半成品重做
		case hasEntry(entries, dbName):
			if !isSQLiteFile(filepath.Join(t, dbName)) {
				return Target{}, errors.New("所选文件夹里的 data.db 不是有效的喵剧数据库，请选择空文件夹或新建一个")
			}
			state = TargetExisting
		default:
			for _, e := range entries {
				if !ignorable[e.Name()] {
					return Target{}, errors.New("所选文件夹不是空的，请选择空文件夹或新建一个")
				}
			}
		}
	case errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		// 不存在（或路径中间有同名文件，交给 checkWritable 给出准确提示）
	default:
		return Target{}, fmt.Errorf("无法访问所选文件夹：%w", err)
	}
	if err := checkWritable(t); err != nil {
		return Target{}, err
	}
	return Target{Path: t, State: state}, nil
}

// Migrate 把 from 的全部数据复制到 to 并校验新数据库。不修改、不删除 from。
func Migrate(from, to string) (err error) {
	if completedFrom(from, to) {
		log.Printf("迁移数据目录：%s 已是 %s 的完整副本（上次迁移已完成），视为成功", to, from)
		return nil
	}
	target, err := ValidateTarget(from, to)
	if err != nil {
		return err
	}
	if err = mkdirAllDurable(target); err != nil {
		return fmt.Errorf("创建新数据目录：%w", err)
	}
	if err = wipe(target); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(target, markerName), []byte("migrating\n"), 0o600); err != nil {
		return fmt.Errorf("写入迁移标记：%w", err)
	}
	if err = syncDir(target); err != nil {
		return fmt.Errorf("写入迁移标记：%w", err)
	}
	defer func() {
		if err != nil {
			// 失败时清掉半成品；清不掉也没关系，标记让下次重做。
			if wipe(target) == nil {
				_ = os.Remove(filepath.Join(target, markerName))
				_ = os.Remove(target) // 只有空目录才会成功
			}
		}
	}()
	entries, err := os.ReadDir(from)
	if err != nil {
		return fmt.Errorf("读取原数据目录：%w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if notCopied[name] || ignorable[name] {
			continue
		}
		src, dst := filepath.Join(from, name), filepath.Join(target, name)
		switch {
		case e.IsDir():
			err = copyTree(src, dst)
		case e.Type().IsRegular():
			err = copyFile(src, dst)
		}
		if err != nil {
			return fmt.Errorf("复制 %s：%w", name, err)
		}
	}
	// 文件在 copyFile 里已逐个 fsync；这里把所有目录项（新建的子目录、文件名）也落盘。
	if err = syncTree(target); err != nil {
		return fmt.Errorf("写入新数据目录：%w", err)
	}
	if err = copyDatabase(from, target); err != nil {
		return err
	}
	// 记下来源，再去掉标记：重跑同一迁移时据此认出“已完成”（completedFrom）。
	if err = writeSynced(filepath.Join(target, migratedFromName), []byte(filepath.Clean(from)+"\n")); err != nil {
		return fmt.Errorf("写入迁移记录：%w", err)
	}
	// 只有新副本全部落盘后才去掉标记；去掉标记本身也要落盘，Cleanup 以“无标记”为前提。
	trace("unmark", target)
	if err = os.Remove(filepath.Join(target, markerName)); err != nil {
		return err
	}
	return syncDir(target)
}

// completedFrom 报告 to 是否已是“从 from 迁来的完整副本”：没有迁移标记、有有效 data.db、来源记录等于 from。
func completedFrom(from, to string) bool {
	to = filepath.Clean(strings.TrimSpace(to))
	if !filepath.IsAbs(to) || !IsDataDir(to) {
		return false
	}
	b, err := os.ReadFile(filepath.Join(to, migratedFromName))
	if err != nil {
		return false
	}
	return samePath(realPath(strings.TrimSpace(string(b))), realPath(from))
}

// writeSynced 创建（覆盖）文件并 fsync。
func writeSynced(p string, body []byte) error {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(body)
	if err == nil {
		trace("fsync", p)
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Cleanup 删除 oldDir 里在 newDir 已有同名副本的条目（数据库的 -wal/-shm 随 data.db 一起删）。
func Cleanup(oldDir, newDir string) error {
	if !filepath.IsAbs(oldDir) || !filepath.IsAbs(newDir) {
		return errors.New("清理旧目录需要绝对路径")
	}
	if samePath(realPath(oldDir), realPath(newDir)) {
		return errors.New("新旧数据目录相同，拒绝清理")
	}
	if rOld, rNew := realPath(oldDir), realPath(newDir); within(rOld, rNew) || within(rNew, rOld) {
		return errors.New("新旧数据目录互相包含，拒绝清理")
	}
	if _, err := os.Stat(filepath.Join(newDir, dbName)); err != nil {
		return fmt.Errorf("新数据目录里没有 data.db，拒绝清理旧目录：%w", err)
	}
	if _, err := os.Lstat(filepath.Join(newDir, markerName)); err == nil {
		return errors.New("新数据目录的迁移尚未完成，拒绝清理旧目录")
	}
	// 删除旧数据前再做一次持久化屏障：确保新副本（含目录项）已在盘上。
	if err := syncTree(newDir); err != nil {
		return fmt.Errorf("确认新数据已写入磁盘失败，拒绝清理旧目录：%w", err)
	}
	entries, err := os.ReadDir(oldDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var failed []string
	for _, e := range entries {
		name := e.Name()
		if ignorable[name] {
			continue
		}
		hasCopy := name == dbName+"-wal" || name == dbName+"-shm"
		if !hasCopy {
			_, statErr := os.Lstat(filepath.Join(newDir, name))
			hasCopy = statErr == nil
		}
		if !hasCopy {
			continue
		}
		trace("remove", filepath.Join(oldDir, name))
		if err := os.RemoveAll(filepath.Join(oldDir, name)); err != nil {
			failed = append(failed, name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("以下内容未能删除：%s", strings.Join(failed, "、"))
	}
	return nil
}

// copyDatabase 用 VACUUM INTO 得到包含 WAL 已提交内容的一致副本，改名后用 quick_check 校验。
func copyDatabase(from, to string) error {
	src := filepath.Join(from, dbName)
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	conn, err := db.Open(src)
	if err != nil {
		return fmt.Errorf("打开原数据库：%w", err)
	}
	tmp := filepath.Join(to, dbTmpName)
	_ = os.Remove(tmp)
	// 目标必须带加密参数，否则副本是明文。
	tmpURI, err := db.EncryptedURI(tmp)
	if err != nil {
		conn.Close()
		return err
	}
	if _, err := conn.Exec(`VACUUM INTO ?`, tmpURI); err != nil {
		conn.Close()
		return fmt.Errorf("复制数据库：%w", err)
	}
	if err := conn.Close(); err != nil {
		return fmt.Errorf("关闭原数据库：%w", err)
	}
	if err := syncFile(tmp); err != nil {
		return fmt.Errorf("写入新数据库：%w", err)
	}
	final := filepath.Join(to, dbName)
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("数据库改名：%w", err)
	}
	if err := syncDir(to); err != nil {
		return fmt.Errorf("数据库改名：%w", err)
	}
	trace("quick_check", final)
	check, err := db.Open(final)
	if err != nil {
		return fmt.Errorf("打开新数据库：%w", err)
	}
	var res string
	qerr := check.QueryRow(`PRAGMA quick_check`).Scan(&res)
	cerr := check.Close()
	if qerr != nil {
		return fmt.Errorf("校验新数据库：%w", qerr)
	}
	if res != "ok" {
		return fmt.Errorf("新数据库校验失败：%s", res)
	}
	return cerr
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o700)
		case d.Type().IsRegular():
			return copyFile(p, target)
		default:
			// 符号链接等特殊文件不复制；记到 stderr 便于事后排查（旧目录随后会被整体清理）。
			log.Printf("迁移数据目录：跳过非普通文件 %s", p)
			return nil
		}
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, in)
	if err == nil && n != st.Size() {
		err = fmt.Errorf("复制不完整：%d/%d 字节", n, st.Size())
	}
	if err == nil {
		trace("fsync", dst)
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// wipe 删除 dir 下除可忽略文件和迁移标记外的全部内容。只对通过 ValidateTarget 的目录调用。
func wipe(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if ignorable[e.Name()] || e.Name() == markerName {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("清理新数据目录：%w", err)
		}
	}
	return nil
}

func hasEntry(entries []os.DirEntry, name string) bool {
	for _, e := range entries {
		if e.Name() == name {
			return true
		}
	}
	return false
}

// checkWritable 在 dir 或其最近的已存在祖先目录里试写一个临时文件。
func checkWritable(dir string) error {
	p := dir
	for {
		st, err := os.Stat(p)
		if err == nil {
			if !st.IsDir() {
				return fmt.Errorf("路径中有同名文件：%s", p)
			}
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			return errors.New("所选位置不可用")
		}
		p = parent
	}
	f, err := os.CreateTemp(p, ".miaoju-write-test-*")
	if err != nil {
		return fmt.Errorf("所选位置没有写入权限：%w", err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// realPath 解析路径中已存在部分的符号链接（macOS 的 /var → /private/var），不存在的尾部原样拼回。
func realPath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// normCase：Windows 与 macOS 默认文件系统不区分大小写。
func normCase(p string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(p)
	}
	return p
}

func samePath(a, b string) bool { return normCase(a) == normCase(b) }

// within 报告 child 是否严格位于 parent 之内。
func within(child, parent string) bool {
	rel, err := filepath.Rel(normCase(parent), normCase(child))
	if err != nil || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// trace 只给测试记录持久化相关调用的顺序。
var trace = func(op, path string) {}

// syncFile 打开文件并 fsync。
func syncFile(p string) error {
	trace("fsync", p)
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

// syncDir fsync 目录本身，让其中新建 / 改名 / 删除的目录项落盘。Windows 不支持对目录 fsync，跳过。
func syncDir(dir string) error {
	trace("fsyncdir", dir)
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

// syncTree fsync dir 下（含 dir）的每个目录。普通文件在复制时已逐个 fsync。
func syncTree(dir string) error {
	var dirs []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 自底向上：子目录先落盘，再落盘包含它的父目录。
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := syncDir(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

// mkdirAllDurable 等同 os.MkdirAll，并对每个新建目录的父目录 fsync，让新目录项本身落盘。
func mkdirAllDurable(dir string) error {
	var created []string
	for p := dir; ; {
		if _, err := os.Stat(p); err == nil {
			break
		}
		created = append(created, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for i := len(created) - 1; i >= 0; i-- {
		if err := syncDir(filepath.Dir(created[i])); err != nil {
			return err
		}
	}
	return nil
}
