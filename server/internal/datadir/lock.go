package datadir

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// MigrateLockFileName 是数据目录迁移 / 清理子命令持有的排他锁文件，放在系统默认数据目录里。
// 锁由执行迁移的 sidecar 子进程自己持有（flock / LockFileEx），进程退出（含被强杀）即由系统释放。
// 不放在壳里：壳退出后迁移子进程可能作为孤儿继续跑，锁必须跟着真正干活的进程走。
// 它和指针文件一样永不复制、永不删除（Windows 上被锁住的字节区间也读不出来）。
const MigrateLockFileName = ".miaoju-migrate.lock"

// errLocked 表示锁正被其他进程持有。
var errLocked = errors.New("locked")

// AcquireMigrateLock 以排他方式锁住 path，最多等待 wait（每 poll 重试一次）。
// 返回的 release 关闭文件并释放锁；拿不到锁时报错，调用方不得继续迁移。
func AcquireMigrateLock(path string, wait, poll time.Duration) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开迁移锁文件: %w", err)
	}
	deadline := time.Now().Add(wait)
	for {
		err = tryLockFile(f)
		if err == nil {
			return func() { _ = f.Close() }, nil // 关闭文件即释放锁
		}
		if !errors.Is(err, errLocked) {
			f.Close()
			return nil, fmt.Errorf("锁定迁移锁文件: %w", err)
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, fmt.Errorf("另一个数据目录迁移仍在进行（%s），请等它结束后重新打开应用", path)
		}
		time.Sleep(poll)
	}
}
