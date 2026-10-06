package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/datadir"
)

// migrateLockWait 是拿不到迁移锁时的最长等待：上一次启动留下的孤儿迁移进程（应用在迁移中途被退出）
// 通常会在这段时间内跑完；仍拿不到就报错，壳把原因记进 lastError，原目录照常可用。
var migrateLockWait = 10 * time.Minute

// withMigrateLock 在 lockPath 非空时先拿排他锁再执行 f（壳总会传 --lock，留空只给测试 / 手工调用）。
// 锁由本进程持有，进程退出（含被强杀）即释放，所以两个迁移 / 清理永远不会并行。
func withMigrateLock(lockPath string, f func() error) error {
	if lockPath == "" {
		return f()
	}
	release, err := datadir.AcquireMigrateLock(lockPath, migrateLockWait, 200*time.Millisecond)
	if err != nil {
		return err
	}
	defer release()
	return f()
}

// runSubcommand 处理壳在正常启动前调用的一次性子命令（数据目录迁移，spec S2）。
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
		if err := fs.Parse(args[1:]); err != nil {
			return true, err
		}
		if *from == "" || *to == "" {
			return true, errors.New("migrate-data-dir 需要 --from 和 --to")
		}
		if err := withMigrateLock(*lock, func() error { return datadir.Migrate(*from, *to) }); err != nil {
			return true, err
		}
		fmt.Fprintln(stdout, "MIGRATED")
		return true, nil
	case "cleanup-data-dir":
		fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
		oldDir := fs.String("old", "", "旧数据目录")
		newDir := fs.String("new", "", "新数据目录")
		lock := fs.String("lock", "", "迁移锁文件（默认数据目录下的 "+datadir.MigrateLockFileName+"）")
		if err := fs.Parse(args[1:]); err != nil {
			return true, err
		}
		if *oldDir == "" || *newDir == "" {
			return true, errors.New("cleanup-data-dir 需要 --old 和 --new")
		}
		if err := withMigrateLock(*lock, func() error { return datadir.Cleanup(*oldDir, *newDir) }); err != nil {
			return true, err
		}
		fmt.Fprintln(stdout, "CLEANED")
		return true, nil
	}
	return false, nil
}
