package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/datadir"
)

// I3：等锁超时 → 专用退出码（壳据此保留待办），等待开始时 stdout 打一行 WAITING_MIGRATE_LOCK。
func TestSubcommandLockTimeoutExitCodeAndWaitingLine(t *testing.T) {
	old := migrateLockWait
	migrateLockWait = 80 * time.Millisecond
	defer func() { migrateLockWait = old }()
	lockPath := filepath.Join(t.TempDir(), datadir.MigrateLockFileName)
	release, err := datadir.AcquireMigrateLock(lockPath, 0, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "dest")
	var out bytes.Buffer
	_, err = runSubcommand([]string{"migrate-data-dir", "--from", from, "--to", to, "--lock", lockPath}, &out)
	if code := exitCodeOf(err); code != exitMigrateLockBusy {
		t.Fatalf("退出码 %d，err=%v", code, err)
	}
	if !strings.Contains(out.String(), waitingLockLine) {
		t.Fatalf("应输出等待行：%q", out.String())
	}
}

// I4：迁移源目录还被一个 sidecar 占着（上一个窗口仍在关闭）时不开始复制，返回“数据目录忙”退出码。
func TestMigrateRefusesWhileSourceInUse(t *testing.T) {
	old := sourceLockWait
	sourceLockWait = 50 * time.Millisecond
	defer func() { sourceLockWait = old }()
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "dest")
	os.WriteFile(filepath.Join(from, "a.txt"), []byte("x"), 0o600)
	release, err := datadir.AcquireInstanceLock(from, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, err = runSubcommand([]string{"migrate-data-dir", "--from", from, "--to", to}, &out)
	if code := exitCodeOf(err); code != exitDataDirBusy {
		t.Fatalf("退出码 %d，err=%v", code, err)
	}
	if _, err := os.Stat(filepath.Join(to, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("源目录被占用时不应复制")
	}
	release()
	if _, err := runSubcommand([]string{"migrate-data-dir", "--from", from, "--to", to}, &out); err != nil {
		t.Fatal(err)
	}
}

// I2：--exit-on-stdin-eof 时 stdin 关闭（壳退出 / 崩溃）就中止子命令，迁移子进程不会变成孤儿。
func TestSubcommandAbortsOnStdinEOF(t *testing.T) {
	r, w := io.Pipe()
	aborted := make(chan struct{})
	oldIn, oldAbort := subcommandStdin, abortSubcommand
	subcommandStdin, abortSubcommand = r, func() { close(aborted) }
	defer func() { subcommandStdin, abortSubcommand = oldIn, oldAbort }()

	// 用一把被占住的迁移锁让子命令卡在等待里，模拟“迁移进行中”
	old := migrateLockWait
	migrateLockWait = 2 * time.Second
	defer func() { migrateLockWait = old }()
	lockPath := filepath.Join(t.TempDir(), datadir.MigrateLockFileName)
	release, _ := datadir.AcquireMigrateLock(lockPath, 0, time.Millisecond)
	defer release()
	done := make(chan struct{})
	go func() {
		defer close(done)
		runSubcommand([]string{"migrate-data-dir", "--from", t.TempDir(), "--to", filepath.Join(t.TempDir(), "d"), "--lock", lockPath, "--exit-on-stdin-eof"}, io.Discard)
	}()
	time.Sleep(50 * time.Millisecond)
	select {
	case <-aborted:
		t.Fatal("stdin 未关闭前不应中止")
	default:
	}
	w.Close()
	select {
	case <-aborted:
	case <-time.After(time.Second):
		t.Fatal("stdin EOF 后应中止")
	}
	<-done
}

func TestExitCodeOf(t *testing.T) {
	if exitCodeOf(nil) != 0 || exitCodeOf(errors.New("x")) != 1 {
		t.Fatal("普通错误退出码应为 1")
	}
	if exitCodeOf(&exitError{code: exitDataDirBusy, err: errors.New("busy")}) != exitDataDirBusy {
		t.Fatal("exitError 应带自己的退出码")
	}
}

// I4：服务模式下数据目录被占用 → exitDataDirBusy，提示明确。
func TestLockDataDirBusy(t *testing.T) {
	dir := t.TempDir()
	release, err := datadir.AcquireInstanceLock(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	old := instanceLockWait
	instanceLockWait = 30 * time.Millisecond
	defer func() { instanceLockWait = old }()
	_, err = lockDataDir(dir)
	if exitCodeOf(err) != exitDataDirBusy || !strings.Contains(err.Error(), "上一个窗口仍在关闭") {
		t.Fatalf("got %v", err)
	}
}
