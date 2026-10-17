// Command miaoju 是喵剧桌面端的画布命令行。
//
// 这是**唯一**调用 os.Exit 的地方：子命令一律返回 typed error，
// 由 app.Execute 统一映射退出码（见 internal/exitcode）。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

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
	// Ctrl+C 取消 context 而不是直接杀进程：进行中的请求随之中止，run --wait 会提示任务仍在服务端运行，
	// 退出码按契约是 7（用户取消）。第一次 Ctrl+C 之后恢复默认处理，再按一次可以强制退出。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	go func() {
		<-ctx.Done()
		stop()
	}()
	root.SetContext(ctx)
	code := app.Execute(root, os.Args[1:], os.Stderr)
	stop()
	os.Exit(code)
}
