package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/datadir"
)

// migrateLockWait 是拿不到迁移锁时的最长等待（另一个迁移进程仍在跑，例如壳异常时没能结束它）。
// 仍拿不到就以 exitMigrateLockBusy 退出：壳保留待办、记下原因，原目录照常可用，下次启动再试。
var migrateLockWait = 10 * time.Minute

// withMigrateLock 在 lockPath 非空时先拿排他锁再执行 f（壳总会传 --lock，留空只给测试 / 手工调用）。
// 锁由本进程持有，进程退出（含被强杀）即释放，所以两个迁移 / 清理永远不会并行。
// 拿不到锁要等待时向 stdout 打 waitingLockLine（拿到后打 lockAcquiredLine）；等待超时返回 exitMigrateLockBusy。
func withMigrateLock(lockPath string, stdout io.Writer, f func() error) error {
	if lockPath == "" {
		return f()
	}
	waited := false
	release, err := datadir.AcquireMigrateLockNotify(lockPath, migrateLockWait, 200*time.Millisecond, func() {
		waited = true
		fmt.Fprintln(stdout, waitingLockLine)
	})
	if errors.Is(err, datadir.ErrMigrateLockBusy) {
		return &exitError{code: exitMigrateLockBusy, err: err}
	}
	if err != nil {
		return err
	}
	defer release()
	if waited {
		fmt.Fprintln(stdout, lockAcquiredLine)
	}
	return f()
}

// migrateWithSourceLock：复制前确认没有 sidecar 还开着源目录（上一个窗口仍在关闭，I4），并在复制期间占住它。
func migrateWithSourceLock(from, to string) error {
	release, err := datadir.AcquireInstanceLock(from, sourceLockWait)
	if errors.Is(err, datadir.ErrDataDirBusy) {
		return &exitError{code: exitDataDirBusy, err: fmt.Errorf("原数据目录仍被上一个窗口的本地服务占用，本次未迁移，下次启动应用时会自动重试（%v）", err)}
	}
	if err != nil {
		return err
	}
	defer release()
	return datadir.Migrate(from, to)
}

// runSubcommand 处理壳在正常启动前调用的一次性子命令（数据目录迁移）。
// 返回 handled=false 表示不是子命令，按正常服务模式继续。子命令不读 stdin、不监听端口。
func runSubcommand(args []string, stdout io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "migrate-data-dir":
		fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
		from := fs.String("from", "", "当前数据目录")
		to := fs.String("to", "", "新数据目录")
		lock := fs.String("lock", "", "迁移锁文件（默认数据目录下的 "+datadir.MigrateLockFileName+"）")
		watch := fs.Bool("exit-on-stdin-eof", false, "stdin 关闭（壳退出）时中止")
		if err := fs.Parse(args[1:]); err != nil {
			return true, err
		}
		if *from == "" || *to == "" {
			return true, errors.New("migrate-data-dir 需要 --from 和 --to")
		}
		if *watch {
			abortOnStdinEOF()
		}
		if err := withMigrateLock(*lock, stdout, func() error { return migrateWithSourceLock(*from, *to) }); err != nil {
			return true, err
		}
		fmt.Fprintln(stdout, "MIGRATED")
		return true, nil
	case "cleanup-data-dir":
		fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
		oldDir := fs.String("old", "", "旧数据目录")
		newDir := fs.String("new", "", "新数据目录")
		lock := fs.String("lock", "", "迁移锁文件（默认数据目录下的 "+datadir.MigrateLockFileName+"）")
		watch := fs.Bool("exit-on-stdin-eof", false, "stdin 关闭（壳退出）时中止")
		if err := fs.Parse(args[1:]); err != nil {
			return true, err
		}
		if *oldDir == "" || *newDir == "" {
			return true, errors.New("cleanup-data-dir 需要 --old 和 --new")
		}
		if *watch {
			abortOnStdinEOF()
		}
		if err := withMigrateLock(*lock, stdout, func() error { return datadir.Cleanup(*oldDir, *newDir) }); err != nil {
			return true, err
		}
		fmt.Fprintln(stdout, "CLEANED")
		return true, nil
	}
	return false, nil
}
