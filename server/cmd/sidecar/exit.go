package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/datadir"
)

// 退出码契约（壳侧见 src-tauri/src/sidecar.rs 的 DATA_DIR_BUSY_EXIT、datadir.rs 的 MIGRATE_LOCK_BUSY_EXIT）。
const (
	// exitDataDirBusy：数据目录正被另一个 sidecar 使用（上一个窗口仍在关闭）。壳提示稍后重试。
	exitDataDirBusy = 75
	// exitMigrateLockBusy：等迁移锁超时（上一次迁移仍在进行）。壳保留待办，下次启动重试。
	exitMigrateLockBusy = 76
	// exitAborted：壳退出（stdin EOF），迁移 / 清理子命令中止。中途中止是安全的：迁移标记让下次重做。
	exitAborted = 130
)

// waitingLockLine：子命令开始等迁移锁时打到 stdout 的一行，壳据此显示“正在等待上一次迁移结束”。
const waitingLockLine = "WAITING_MIGRATE_LOCK"

// lockAcquiredLine：等过锁之后终于拿到时打到 stdout 的一行，壳据此切回“正在迁移”。
const lockAcquiredLine = "MIGRATE_LOCK_ACQUIRED"

// instanceLockWait：服务模式拿数据目录锁的最长等待。上一个 sidecar 正常收尾不超过 3 秒（壳随后强杀），
// 这里多等一会儿，用户在退出后立刻重开时多数情况下不用点“重试”。
var instanceLockWait = 4 * time.Second

// sourceLockWait：迁移前确认没有 sidecar 还开着源目录的最长等待。
var sourceLockWait = 10 * time.Second

// exitError 让 main 以指定退出码退出。
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// exitCodeOf：nil → 0；exitError → 它的退出码；其他错误 → 1。
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return 1
}

// lockDataDir 在打开 data.db 前锁住数据目录（I4）。被占用时返回 exitDataDirBusy。
func lockDataDir(dir string) (func(), error) {
	release, err := datadir.AcquireInstanceLock(dir, instanceLockWait)
	if errors.Is(err, datadir.ErrDataDirBusy) {
		return nil, &exitError{code: exitDataDirBusy, err: fmt.Errorf("上一个窗口仍在关闭，请稍后重试（%v）", err)}
	}
	return release, err
}

// subcommandStdin / abortSubcommand 供测试替换。
var (
	subcommandStdin io.Reader = os.Stdin
	abortSubcommand           = func() { os.Exit(exitAborted) }
)

// abortOnStdinEOF：壳把子进程的 stdin 接成管道并一直开着；壳退出或崩溃时管道关闭，这里读到 EOF 就中止，
// 迁移子进程不会变成孤儿（I2）。Windows 上壳另外用 Job Object 兜底。
func abortOnStdinEOF() {
	go func() {
		_, _ = io.Copy(io.Discard, subcommandStdin)
		log.Print("壳已退出（stdin 关闭），中止数据目录子命令")
		abortSubcommand()
	}()
}
