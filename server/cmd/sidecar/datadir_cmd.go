package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/james/huabu_studio/apps/api/internal/datadir"
)

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
		if err := fs.Parse(args[1:]); err != nil {
			return true, err
		}
		if *from == "" || *to == "" {
			return true, errors.New("migrate-data-dir 需要 --from 和 --to")
		}
		if err := datadir.Migrate(*from, *to); err != nil {
			return true, err
		}
		fmt.Fprintln(stdout, "MIGRATED")
		return true, nil
	case "cleanup-data-dir":
		fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
		oldDir := fs.String("old", "", "旧数据目录")
		newDir := fs.String("new", "", "新数据目录")
		if err := fs.Parse(args[1:]); err != nil {
			return true, err
		}
		if *oldDir == "" || *newDir == "" {
			return true, errors.New("cleanup-data-dir 需要 --old 和 --new")
		}
		if err := datadir.Cleanup(*oldDir, *newDir); err != nil {
			return true, err
		}
		fmt.Fprintln(stdout, "CLEANED")
		return true, nil
	}
	return false, nil
}
