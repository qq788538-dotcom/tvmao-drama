package datadir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MigrateLockFileName 是数据目录迁移 / 清理子命令持有的排他锁文件，放在系统默认数据目录里。
// 锁由执行迁移的 sidecar 子进程自己持有（flock / LockFileEx），进程退出（含被强杀）即由系统释放。
// 不放在壳里：锁必须跟着真正干活的进程走（壳崩溃时子进程会因 stdin EOF 自行退出，见 cmd/sidecar）。
// 它和指针文件一样永不复制、永不删除（Windows 上被锁住的字节区间也读不出来）。
const MigrateLockFileName = ".miaoju-migrate.lock"

// InstanceLockFileName 是服务模式 sidecar 在数据目录里持有的排他锁文件：同一数据目录同一时刻只允许
// 一个 sidecar 打开 data.db。进程退出（含被强杀）即由系统释放；同样永不复制、不计入“目录非空”。
const InstanceLockFileName = ".miaoju-instance.lock"

// ErrMigrateLockBusy：等迁移锁超时（上一次迁移仍在进行）。调用方据此保留待办、下次启动重试。
var ErrMigrateLockBusy = errors.New("migrate lock busy")

// ErrDataDirBusy：数据目录正被另一个 sidecar 使用（通常是上一个窗口仍在关闭）。
var ErrDataDirBusy = errors.New("data dir busy")

// kindError 让 errors.Is 认出 kind，而 Error() 只给用户看的中文文案（不带英文哨兵前缀）。
type kindError struct {
	kind error
	msg  string
}

func (e *kindError) Error() string { return e.msg }
func (e *kindError) Unwrap() error { return e.kind }

// errLocked 表示锁正被其他进程持有。
var errLocked = errors.New("locked")

// AcquireMigrateLock 以排他方式锁住 path，最多等待 wait（每 poll 重试一次）。
// 返回的 release 关闭文件并释放锁；拿不到锁时报错（errors.Is ErrMigrateLockBusy），调用方不得继续迁移。
func AcquireMigrateLock(path string, wait, poll time.Duration) (release func(), err error) {
	return AcquireMigrateLockNotify(path, wait, poll, nil)
}

// AcquireMigrateLockNotify 同 AcquireMigrateLock；第一次没拿到锁、开始等待时调用一次 onWait（可为 nil）。
func AcquireMigrateLockNotify(path string, wait, poll time.Duration, onWait func()) (func(), error) {
	release, err := acquireFileLock(path, wait, poll, onWait)
	if errors.Is(err, errLocked) {
		return nil, &kindError{kind: ErrMigrateLockBusy, msg: fmt.Sprintf("另一个数据目录迁移仍在进行（%s），已等待 %s 仍未结束。本次继续使用原数据目录，下次启动应用时会自动重试迁移",
			path, wait.Round(time.Second))}
	}
	if err != nil {
		return nil, fmt.Errorf("迁移锁 %s: %w", path, err)
	}
	return release, nil
}

// AcquireInstanceLock 锁住 dataDir 下的 InstanceLockFileName，最多等 wait。被占用时返回 ErrDataDirBusy。
func AcquireInstanceLock(dataDir string, wait time.Duration) (func(), error) {
	path := filepath.Join(dataDir, InstanceLockFileName)
	release, err := acquireFileLock(path, wait, 100*time.Millisecond, nil)
	if errors.Is(err, errLocked) {
		return nil, &kindError{kind: ErrDataDirBusy, msg: fmt.Sprintf("数据目录 %s 正被另一个喵剧AI 本地服务使用", dataDir)}
	}
	if err != nil {
		return nil, fmt.Errorf("数据目录锁 %s: %w", path, err)
	}
	return release, nil
}

func acquireFileLock(path string, wait, poll time.Duration, onWait func()) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开锁文件: %w", err)
	}
	deadline := time.Now().Add(wait)
	waited := false
	for {
		err = tryLockFile(f)
		if err == nil {
			return func() { _ = f.Close() }, nil // 关闭文件即释放锁
		}
		if !errors.Is(err, errLocked) {
			f.Close()
			return nil, fmt.Errorf("锁定: %w", err)
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, errLocked
		}
		if !waited && onWait != nil {
			onWait()
		}
		waited = true
		time.Sleep(poll)
	}
}
