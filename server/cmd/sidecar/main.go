// sidecar 是桌面端的本地服务进程，由 Tauri 壳拉起。
//
// 启动协议（壳与 sidecar 的唯一契约，改动要两边一起改，壳侧见 src-tauri/src/lib.rs）：
//  1. 壳通过 stdin 写入一行 token；
//  2. sidecar 打开 <data-dir>/data.db 并迁移，监听 127.0.0.1 的随机端口，就绪后向 stdout 打印 "READY <port>"；
//  3. 之后 stdin 上读到一行 "SHUTDOWN"（壳正常退出）或 EOF（壳崩溃、被强杀），两者走同一条优雅退出路径：
//     取消 ctx、停止接收请求、等后台任务退出、关闭数据库后以 0 退出，整个过程不超过 shutdownTimeout。
//
// token 走 stdin 而不是参数或环境变量：后两者同机其他进程读得到。
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	nethttp "net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db"
	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

// version 由构建脚本通过 -ldflags "-X main.version=…" 注入。
var version = "dev"

// defaultOrigins 只含打包后的两个来源。开发服务器 http://localhost:5173 由壳在 debug 构建里
// 经 --origins 传入（P0 遗留：release 包不能放行它）。
const defaultOrigins = "tauri://localhost,http://tauri.localhost"

const (
	maxTokenLen = 256
	// shutdownTimeout 是优雅退出的总时长上限（HTTP 收尾 + 等 worker）。壳在 3 秒后强杀，
	// 超过它的部分没有意义；到点后不再等待，直接关库退出。
	shutdownTimeout      = 3 * time.Second
	projectCleanInterval = 30 * time.Second
	orphanSweepInterval  = 24 * time.Hour
)

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
	if err := ensurePrivateDir(*dataDir); err != nil {
		return fmt.Errorf("创建数据目录: %w", err)
	}

	stdin := bufio.NewReader(os.Stdin)
	token, err := readToken(stdin)
	if err != nil {
		return err
	}

	conn, err := db.Open(filepath.Join(*dataDir, "data.db"))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		return fmt.Errorf("数据库迁移: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("监听端口: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	mediaToken, err := randomHex(32)
	if err != nil {
		ln.Close()
		return fmt.Errorf("生成媒体 token: %w", err)
	}
	storage, err := assets.NewLocalStorage(assets.LocalConfig{
		Root:    filepath.Join(*dataDir, "media"),
		BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		Token:   mediaToken,
	})
	if err != nil {
		ln.Close()
		return err
	}

	a, err := wire(conn, storage, localauth.Config{
		Token:          token,
		MediaToken:     mediaToken,
		AllowedOrigins: splitOrigins(*origins),
	}, *dataDir, version)
	if err != nil {
		ln.Close()
		return err
	}
	apphttp.MustWire(a.deps)
	srv := &nethttp.Server{
		Handler: apphttp.NewRouter(a.deps),
		// P0 遗留：防慢速请求头占满连接。
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workers := []<-chan struct{}{
		a.cleaner.Start(ctx, projectCleanInterval),
		a.sweeper.Start(ctx, orphanSweepInterval, library.DefaultGracePeriod),
	}
	// 生成后台：先把上个进程的租约判过期并续跑在途任务，再起队列/投递/巡检循环（spec S7）。
	// 在 READY 之前调用；退出时随 ctx 取消停下，没跑完的任务由下次启动续跑。
	a.gen.Gen.StartBackground(ctx)

	go func() {
		waitForShutdown(stdin)
		log.Print("收到退出信号（stdin），sidecar 正在关闭")
		cancel()
	}()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigs:
			log.Print("收到系统信号，sidecar 正在关闭")
			cancel()
		case <-ctx.Done():
		}
	}()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	fmt.Printf("READY %d\n", port)

	select {
	case err := <-serveErr:
		cancel()
		waitDone(shutdownTimeout, workers...)
		return err
	case <-ctx.Done():
	}
	// 关闭顺序：ctx 已取消（worker 不再开始新一轮）→ 停 HTTP → 等 worker 退出 → 关数据库（defer）。
	// 必须等 worker 退出再关库：sql.DB.Close 不会等事务以外的后续语句，worker 剩下的步骤只会拿到
	// "sql: database is closed"。
	deadline := time.Now().Add(shutdownTimeout)
	sctx, scancel := context.WithDeadline(context.Background(), deadline)
	defer scancel()
	if err := srv.Shutdown(sctx); err != nil {
		log.Printf("关闭 HTTP 服务: %v", err)
	}
	if !waitDone(time.Until(deadline), workers...) {
		log.Print("等待后台任务退出超时，直接关闭数据库")
	}
	log.Print("sidecar 已关闭")
	return nil
}

// waitDone 等 chans 全部关闭，最多等 timeout；全部关闭返回 true。
func waitDone(timeout time.Duration, chans ...<-chan struct{}) bool {
	var wg sync.WaitGroup
	for _, c := range chans {
		wg.Add(1)
		go func(c <-chan struct{}) {
			defer wg.Done()
			<-c
		}(c)
	}
	all := make(chan struct{})
	go func() {
		wg.Wait()
		close(all)
	}()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-all:
		return true
	case <-t.C:
		return false
	}
}

// ensurePrivateDir 建数据目录并收紧成仅当前用户可访问（data.db 里有 API Key，spec M3）。
// P0 用 0755 建过的老目录也在这里改掉。Windows 的 %APPDATA% 本身是用户私有目录，chmod 只管只读位。
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chmod(dir, 0o700)
}

// readToken 读 stdin 第一行。ReadSlice 受 bufio 默认 4096 字节缓冲限制，超长输入直接报错，
// 不会把任意长的一行读进内存。TrimSpace 同时处理 Windows 的 \r\n。
func readToken(r *bufio.Reader) (string, error) {
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return "", fmt.Errorf("token 长度超过 %d", maxTokenLen)
	}
	token := strings.TrimSpace(string(line))
	if token == "" && err != nil {
		return "", fmt.Errorf("读取 token: %w", err)
	}
	if len(token) < 32 {
		return "", errors.New("token 长度不足 32")
	}
	if len(token) > maxTokenLen {
		return "", fmt.Errorf("token 长度超过 %d", maxTokenLen)
	}
	return token, nil
}

// waitForShutdown 阻塞到 stdin 上出现一行 SHUTDOWN 或 EOF（壳退出、崩溃、被强杀都会关管道）。
func waitForShutdown(r *bufio.Reader) {
	for {
		line, err := r.ReadString('\n')
		if strings.TrimSpace(line) == "SHUTDOWN" || err != nil {
			return
		}
	}
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
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
