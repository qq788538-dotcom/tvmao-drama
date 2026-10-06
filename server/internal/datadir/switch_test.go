package datadir_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/datadir"
)

// I1：设置页选目录时区分三种情况：空（迁移过去）/ 已有喵剧数据（直接使用）/ 非空且无关（拒绝）。
func TestInspectChosenFolderStates(t *testing.T) {
	cur := t.TempDir()

	empty := t.TempDir()
	got, err := datadir.InspectChosenFolder(cur, empty)
	if err != nil || got.State != datadir.TargetEmpty || got.Path != filepath.Join(empty, datadir.DedicatedSubdir) {
		t.Fatalf("空文件夹：%+v %v", got, err)
	}

	existing := t.TempDir()
	sub := filepath.Join(existing, datadir.DedicatedSubdir)
	os.MkdirAll(sub, 0o700)
	newSourceDB(t, sub)
	mustWrite(t, filepath.Join(sub, "media", "a.png"), "PNG")
	got, err = datadir.InspectChosenFolder(cur, existing)
	if err != nil || got.State != datadir.TargetExisting || got.Path != sub {
		t.Fatalf("已有喵剧数据：%+v %v", got, err)
	}
	// 直接选中 MiaojuData 本身也认
	got, err = datadir.InspectChosenFolder(cur, sub)
	if err != nil || got.State != datadir.TargetExisting || got.Path != sub {
		t.Fatalf("直接选 MiaojuData：%+v %v", got, err)
	}

	// 上次迁移没做完（有迁移中标记）：不是可直接使用的数据目录，仍按迁移目标处理（迁移会重做）
	half := t.TempDir()
	hsub := filepath.Join(half, datadir.DedicatedSubdir)
	os.MkdirAll(hsub, 0o700)
	newSourceDB(t, hsub)
	mustWrite(t, filepath.Join(hsub, ".miaoju-migrating"), "migrating\n")
	got, err = datadir.InspectChosenFolder(cur, half)
	if err != nil || got.State != datadir.TargetEmpty {
		t.Fatalf("迁移未完成的目录：%+v %v", got, err)
	}

	unrelated := t.TempDir()
	mustWrite(t, filepath.Join(unrelated, datadir.DedicatedSubdir, "x.txt"), "x")
	if _, err := datadir.InspectChosenFolder(cur, unrelated); err == nil || !strings.Contains(err.Error(), "不是空的") {
		t.Fatalf("非空且无关应拒绝：%v", err)
	}

	fake := t.TempDir()
	mustWrite(t, filepath.Join(fake, datadir.DedicatedSubdir, "data.db"), "not sqlite")
	if _, err := datadir.InspectChosenFolder(cur, fake); err == nil || !strings.Contains(err.Error(), "data.db") {
		t.Fatalf("data.db 不是 SQLite 应拒绝：%v", err)
	}

	// 当前目录本身不能再选
	if _, err := datadir.InspectChosenFolder(sub, existing); err == nil || !strings.Contains(err.Error(), "相同") {
		t.Fatalf("选回当前目录应报相同：%v", err)
	}
}

// 已有喵剧数据的目录不能作为迁移目标（迁移会把它清空）。
func TestValidateTargetRejectsExistingData(t *testing.T) {
	cur, target := t.TempDir(), t.TempDir()
	newSourceDB(t, target)
	if _, err := datadir.ValidateTarget(cur, target); err == nil || !strings.Contains(err.Error(), "已有喵剧数据") {
		t.Fatalf("got %v", err)
	}
}

// I2：迁移子进程在壳退出后把迁移做完（标记已去掉），下次启动重跑同一迁移应视为成功，不清空新副本。
func TestMigrateIsIdempotentAfterCompletedCopy(t *testing.T) {
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "dest")
	newSourceDB(t, from)
	mustWrite(t, filepath.Join(from, "media", "a.png"), "PNG")
	if err := datadir.Migrate(from, to); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(to, "media", "b.png"), "新目录里后来的文件")
	if err := datadir.Migrate(from, to); err != nil {
		t.Fatalf("同一迁移已完成，应视为成功：%v", err)
	}
	if !exists(filepath.Join(to, "media", "b.png")) || !exists(filepath.Join(to, "data.db")) {
		t.Fatal("重跑不应清空已完成的副本")
	}
	// 记录不会被继续复制：从新目录再迁走时，下一个目录里的记录指向新目录
	next := filepath.Join(t.TempDir(), "next")
	if err := datadir.Migrate(to, next); err != nil {
		t.Fatal(err)
	}
	if err := datadir.Migrate(to, next); err != nil {
		t.Fatalf("第二段迁移也应幂等：%v", err)
	}
	// 来源不同（不是这次迁移的产物）→ 照常拒绝
	other := t.TempDir()
	if err := datadir.Migrate(other, to); err == nil {
		t.Fatal("别的来源迁到已有数据的目录应拒绝")
	}
}

// I4：同一数据目录只允许一个 sidecar；锁文件不影响空目录判断、不被复制。
func TestInstanceLockIsExclusive(t *testing.T) {
	dir := t.TempDir()
	release, err := datadir.AcquireInstanceLock(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := datadir.AcquireInstanceLock(dir, 50*time.Millisecond); !errors.Is(err, datadir.ErrDataDirBusy) {
		t.Fatalf("已被占用应返回 ErrDataDirBusy：%v", err)
	}
	if _, err := datadir.ValidateTarget(t.TempDir(), dir); err != nil {
		t.Fatalf("只有锁文件的目录应视为空：%v", err)
	}
	mustWrite(t, filepath.Join(dir, "keep.txt"), "x")
	to := filepath.Join(t.TempDir(), "dest")
	if err := datadir.Migrate(dir, to); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(to, datadir.InstanceLockFileName)) {
		t.Fatal("实例锁文件不应被复制")
	}
	release()
	r2, err := datadir.AcquireInstanceLock(dir, 0)
	if err != nil {
		t.Fatalf("释放后应能拿到：%v", err)
	}
	r2()
}

// I3：等锁超时返回可识别的错误；开始等待时通知一次（壳据此显示“正在等待上一次迁移结束”）。
func TestMigrateLockTimeoutIsRecognizableAndNotifiesWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), datadir.MigrateLockFileName)
	release, err := datadir.AcquireMigrateLock(path, 0, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	waits := 0
	_, err = datadir.AcquireMigrateLockNotify(path, 60*time.Millisecond, 10*time.Millisecond, func() { waits++ })
	if !errors.Is(err, datadir.ErrMigrateLockBusy) {
		t.Fatalf("超时应返回 ErrMigrateLockBusy：%v", err)
	}
	if !strings.Contains(err.Error(), "下次启动") {
		t.Fatalf("提示应说明下次启动会重试：%v", err)
	}
	if waits != 1 {
		t.Fatalf("onWait 应调用一次，got %d", waits)
	}
	// 一次就拿到锁时不通知
	p2 := filepath.Join(t.TempDir(), datadir.MigrateLockFileName)
	r2, err := datadir.AcquireMigrateLockNotify(p2, time.Second, 10*time.Millisecond, func() { t.Fatal("不应等待") })
	if err != nil {
		t.Fatal(err)
	}
	r2()
}
