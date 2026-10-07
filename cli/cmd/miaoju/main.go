// Command miaoju 是喵剧桌面端的画布命令行。
//
// 这是**唯一**调用 os.Exit 的地方：子命令一律返回 typed error，
// 由 app.Execute 统一映射退出码（见 internal/exitcode）。
package main

import (
	"fmt"
	"os"

	"github.com/james/huabu_studio/apps/cli/internal/app"
)

// version 由构建时 ldflags 注入：
//
//	go build -ldflags "-X main.version=1.2.0"
var version = "dev"

func main() {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法确定当前目录：%v\n", err)
		os.Exit(2)
	}

	root := app.NewRootCommand(app.Dependencies{
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Version: version,
		WorkDir: wd,
	})
	os.Exit(app.Execute(root, os.Args[1:], os.Stderr))
}
