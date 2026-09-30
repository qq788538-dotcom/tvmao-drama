// sidecar 是桌面端的本地服务进程，由 Tauri 壳拉起。
//
// 启动协议（壳与 sidecar 的唯一契约，改动要两边一起改）：
//  1. 壳通过 stdin 写入一行 token；
//  2. sidecar 监听 127.0.0.1 的随机端口，就绪后向 stdout 打印 "READY <port>"；
//  3. stdin 读到 EOF 说明壳已退出，sidecar 随之退出。
//
// token 走 stdin 而不是参数或环境变量：后两者同机其他进程读得到。
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	nethttp "net/http"
	"os"
	"strings"

	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

// version 由构建脚本通过 -ldflags "-X main.version=…" 注入。
var version = "dev"

const defaultOrigins = "tauri://localhost,http://tauri.localhost,http://localhost:5173"

func main() {
	log.SetOutput(os.Stderr)
	if err := run(); err != nil {
		log.Printf("sidecar 启动失败: %v", err)
		os.Exit(1)
	}
}

func run() error {
	dataDir := flag.String("data-dir", "", "数据目录（必填）")
	origins := flag.String("origins", defaultOrigins, "允许的 Origin，逗号分隔")
	flag.Parse()
	if *dataDir == "" {
		return errors.New("缺少 --data-dir")
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return fmt.Errorf("创建数据目录: %w", err)
	}

	stdin := bufio.NewReader(os.Stdin)
	token, err := readToken(stdin)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("监听端口: %w", err)
	}
	handler := apphttp.NewRouter(apphttp.Deps{
		Auth:    localauth.Config{Token: token, AllowedOrigins: splitOrigins(*origins)},
		DataDir: *dataDir,
		Version: version,
	})

	// 壳退出（含崩溃、被强杀）时 stdin 管道关闭，这里读到 EOF 就跟着退出。
	go func() {
		_, _ = io.Copy(io.Discard, stdin)
		log.Print("stdin 已关闭，sidecar 退出")
		os.Exit(0)
	}()

	fmt.Printf("READY %d\n", ln.Addr().(*net.TCPAddr).Port)
	return nethttp.Serve(ln, handler)
}

// readToken 读 stdin 第一行。TrimSpace 同时处理 Windows 的 \r\n。
func readToken(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	token := strings.TrimSpace(line)
	if token == "" && err != nil {
		return "", fmt.Errorf("读取 token: %w", err)
	}
	if len(token) < 32 {
		return "", errors.New("token 长度不足 32")
	}
	return token, nil
}

func splitOrigins(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}
