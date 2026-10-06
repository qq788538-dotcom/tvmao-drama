package datadir_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/datadir"
)

func TestMigrateLockIsExclusiveAndReleasable(t *testing.T) {
	path := filepath.Join(t.TempDir(), datadir.MigrateLockFileName)
	release, err := datadir.AcquireMigrateLock(path, 0, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := datadir.AcquireMigrateLock(path, 80*time.Millisecond, 10*time.Millisecond); err == nil ||
		!strings.Contains(err.Error(), "另一个数据目录迁移仍在进行") {
		t.Fatalf("锁被占用时应报错，got %v", err)
	}
	release()
	r2, err := datadir.AcquireMigrateLock(path, 0, time.Millisecond)
	if err != nil {
		t.Fatalf("释放后应能拿到锁：%v", err)
	}
	r2()
}

func TestMigrateLockWaitsForHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), datadir.MigrateLockFileName)
	release, err := datadir.AcquireMigrateLock(path, 0, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(100 * time.Millisecond); release() }()
	r2, err := datadir.AcquireMigrateLock(path, 3*time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("持有者释放后等待方应拿到锁：%v", err)
	}
	r2()
}

// 子进程持锁后被强杀（模拟迁移进程崩溃 / 被结束），锁应由系统释放。
func TestMigrateLockReleasedWhenHolderProcessDies(t *testing.T) {
	if p := os.Getenv("MIAOJU_LOCK_HOLDER"); p != "" {
		if _, err := datadir.AcquireMigrateLock(p, 0, time.Millisecond); err != nil {
			os.Exit(3)
		}
		os.Stdout.WriteString("LOCKED\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), datadir.MigrateLockFileName)
	cmd := exec.Command(os.Args[0], "-test.run=^TestMigrateLockReleasedWhenHolderProcessDies$")
	cmd.Env = append(os.Environ(), "MIAOJU_LOCK_HOLDER="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 7)
	if _, err := out.Read(buf); err != nil || string(buf) != "LOCKED\n" {
		cmd.Process.Kill()
		t.Fatalf("子进程未拿到锁：%q %v", buf, err)
	}
	if _, err := datadir.AcquireMigrateLock(path, 50*time.Millisecond, 10*time.Millisecond); err == nil {
		cmd.Process.Kill()
		t.Fatal("另一进程持锁时不应拿到锁")
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	release, err := datadir.AcquireMigrateLock(path, 2*time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("持锁进程退出后应能拿到锁：%v", err)
	}
	release()
}

// 锁文件在默认数据目录里，默认目录通常就是迁移源：不能被复制，也不能让目录判为非空。
func TestMigrateDoesNotCopyLockFile(t *testing.T) {
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "dest")
	release, err := datadir.AcquireMigrateLock(filepath.Join(from, datadir.MigrateLockFileName), 0, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := os.WriteFile(filepath.Join(from, "keep.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := datadir.Migrate(from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(to, datadir.MigrateLockFileName)); !os.IsNotExist(err) {
		t.Fatalf("锁文件不应被复制：%v", err)
	}
	if _, err := os.Stat(filepath.Join(to, "keep.txt")); err != nil {
		t.Fatalf("普通文件应被复制：%v", err)
	}
}
