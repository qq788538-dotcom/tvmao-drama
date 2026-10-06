package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/db"
)

const testToken = "0123456789abcdef0123456789abcdef"

func TestReadToken(t *testing.T) {
	got, err := readToken(bufio.NewReader(strings.NewReader(testToken + "\n")))
	if err != nil || got != testToken {
		t.Fatalf("got %q, %v", got, err)
	}
}

// Windows 管道写入的行尾是 \r\n，不能把 \r 算进 token。
func TestReadTokenTrimsCRLF(t *testing.T) {
	got, err := readToken(bufio.NewReader(strings.NewReader(testToken + "\r\n")))
	if err != nil || got != testToken {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadTokenRejectsShort(t *testing.T) {
	if _, err := readToken(bufio.NewReader(strings.NewReader("short\n"))); err == nil {
		t.Fatal("过短的 token 应当报错")
	}
}

func TestReadTokenRejectsEOF(t *testing.T) {
	if _, err := readToken(bufio.NewReader(strings.NewReader(""))); err == nil {
		t.Fatal("stdin 直接关闭应当报错")
	}
}

// buildSidecar 把当前包编译成真实二进制，端到端测试握手协议。
func buildSidecar(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "sidecar")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// startSidecar 启动进程、写入 token、等到 READY，返回进程、stdin 和端口。
func startSidecar(t *testing.T, bin, dataDir string, extraArgs ...string) (*exec.Cmd, io.WriteCloser, string) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"--data-dir", dataDir}, extraArgs...)...)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	fmt.Fprintf(stdin, "%s\n", testToken)

	ready := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if port, ok := strings.CutPrefix(sc.Text(), "READY "); ok {
				ready <- strings.TrimSpace(port)
				return
			}
		}
		ready <- ""
	}()
	select {
	case port := <-ready:
		if port == "" {
			t.Fatal("sidecar 退出前没有打印 READY")
		}
		return cmd, stdin, port
	case <-time.After(10 * time.Second):
		t.Fatal("等待 READY 超时")
	}
	return nil, nil, ""
}

func TestHandshakeAndPing(t *testing.T) {
	bin := buildSidecar(t)
	_, _, port := startSidecar(t, bin, t.TempDir())

	resp, err := http.Get("http://127.0.0.1:" + port + "/api/v1/ping?token=" + testToken)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ping code = %d", resp.StatusCode)
	}

	// 没带 token 必须 401——确认握手读到的 token 真的生效了。
	resp, err = http.Get("http://127.0.0.1:" + port + "/api/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无 token code = %d, want 401", resp.StatusCode)
	}
}

// Tauri 进程崩溃时 stdin 管道会被关闭；sidecar 必须跟着退出，不能变成孤儿。
// EOF 走和 SHUTDOWN 相同的优雅退出路径：数据库正常关闭（WAL 被检查点合并并删除），不是直接 os.Exit。
func TestExitsWhenStdinCloses(t *testing.T) {
	bin := buildSidecar(t)
	dataDir := t.TempDir()
	cmd, stdin, _ := startSidecar(t, bin, dataDir)
	stdin.Close()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("退出码非 0: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stdin 关闭 5 秒后 sidecar 仍未退出")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "data.db-wal")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("data.db-wal 仍在，数据库没有正常关闭: %v", err)
	}
}

// waitDone 等所有后台 worker 退出，超时返回 false（main 不会因为卡住的 worker 无限挂起）。
func TestWaitDone(t *testing.T) {
	a, b := make(chan struct{}), make(chan struct{})
	close(a)
	close(b)
	if !waitDone(time.Second, a, b) {
		t.Fatal("全部关闭时应返回 true")
	}
	stuck := make(chan struct{})
	start := time.Now()
	if waitDone(50*time.Millisecond, a, stuck) {
		t.Fatal("有 worker 未退出时应超时返回 false")
	}
	if time.Since(start) > time.Second {
		t.Fatal("超时没有生效")
	}
}

// 数据目录建不出来时要明确失败，不能打印 READY 让壳误以为启动成功。
func TestFailsWithoutReadyWhenDataDirUnusable(t *testing.T) {
	bin := buildSidecar(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--data-dir", filepath.Join(blocker, "sub"))
	stdin, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(stdin, "%s\n", testToken)
	stdout, _ := io.ReadAll(out)
	err := cmd.Wait()
	if err == nil {
		t.Fatal("应当以非 0 退出")
	}
	if strings.Contains(string(stdout), "READY") {
		t.Fatalf("失败时不应打印 READY，stdout=%q", stdout)
	}
}

func TestReadTokenRejectsTooLong(t *testing.T) {
	for _, n := range []int{300, 5000} {
		if _, err := readToken(bufio.NewReader(strings.NewReader(strings.Repeat("a", n) + "\n"))); err == nil {
			t.Fatalf("%d 字节的 token 应被拒绝", n)
		}
	}
}

func TestWaitForShutdownReturnsOnShutdownLineOrEOF(t *testing.T) {
	done := make(chan struct{})
	go func() {
		waitForShutdown(bufio.NewReader(strings.NewReader("noise\nSHUTDOWN\r\nmore\n")))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("读到 SHUTDOWN 行后应返回")
	}
	waitForShutdown(bufio.NewReader(strings.NewReader(""))) // EOF 立即返回
}

// 壳正常退出时写一行 SHUTDOWN：sidecar 关库后以 0 退出，库文件已建好。
func TestShutdownLineExitsCleanly(t *testing.T) {
	bin := buildSidecar(t)
	dataDir := t.TempDir()
	cmd, stdin, _ := startSidecar(t, bin, dataDir)
	fmt.Fprintln(stdin, "SHUTDOWN")
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("退出码非 0: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SHUTDOWN 后 5 秒仍未退出")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "data.db")); err != nil {
		t.Fatalf("data.db 没有建在数据目录: %v", err)
	}
}

// P0 遗留：P0 用 0755 建过目录的老安装，再启动时要收紧成 0700。
func TestDataDirBecomesPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 的 %APPDATA% 本身是用户私有目录，chmod 只管只读位")
	}
	bin := buildSidecar(t)
	dataDir := filepath.Join(t.TempDir(), "com.miaoju.desktop")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dataDir, 0o755)
	startSidecar(t, bin, dataDir)
	st, err := os.Stat(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("perm=%v want 0700", st.Mode().Perm())
	}
}

// P0 遗留：开发服务器的来源只在壳的 debug 构建经 --origins 传入，默认白名单里没有它。
func TestDefaultOriginsExcludeDevServer(t *testing.T) {
	bin := buildSidecar(t)
	_, _, port := startSidecar(t, bin, t.TempDir())
	check := func(origin string, want int) {
		req, _ := http.NewRequest("GET", "http://127.0.0.1:"+port+"/api/v1/ping", nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Origin", origin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("Origin %s: code=%d want %d", origin, resp.StatusCode, want)
		}
	}
	check("tauri://localhost", http.StatusOK)
	check("http://tauri.localhost", http.StatusOK)
	check("http://localhost:5173", http.StatusForbidden)
}

func TestOriginsFlagOverridesDefault(t *testing.T) {
	bin := buildSidecar(t)
	_, _, port := startSidecar(t, bin, t.TempDir(), "--origins", "http://localhost:5173")
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+port+"/api/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Origin", "http://localhost:5173")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("code=%d want 200", resp.StatusCode)
	}
}

// P0 遗留：启动失败必须以退出码 1 结束，并在 stderr 留下原因（壳据此显示错误页）。
func TestStartupFailuresExitOneWithStderr(t *testing.T) {
	bin := buildSidecar(t)
	cases := []struct {
		name  string
		args  []string
		stdin string
	}{
		{"缺 --data-dir", nil, testToken + "\n"},
		{"token 过短", []string{"--data-dir", t.TempDir()}, "short\n"},
		{"stdin 直接关闭", []string{"--data-dir", t.TempDir()}, ""},
	}
	for _, c := range cases {
		cmd := exec.Command(bin, c.args...)
		cmd.Stdin = strings.NewReader(c.stdin)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			t.Fatalf("%s: err=%v，want 退出码 1", c.name, err)
		}
		if strings.TrimSpace(stderr.String()) == "" {
			t.Fatalf("%s: stderr 为空", c.name)
		}
	}
}

// 首次启动在打印 READY 之前就已把内置模型和配置导入 data.db（spec M9）。
func TestBuiltinSeedImportedOnStartup(t *testing.T) {
	bin := buildSidecar(t)
	dataDir := t.TempDir()
	startSidecar(t, bin, dataDir)
	conn, err := db.Open(filepath.Join(dataDir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var models, settings int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM model_configs`).Scan(&models); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM settings WHERE "key" IN ('base_domain', 'canvas_light_control', 'canvas_multi_angle', 'canvas_postprocess_config', 'canvas_script_config', 'gen_image_prompt_presets')`).Scan(&settings); err != nil {
		t.Fatal(err)
	}
	if models != 29 || settings != 6 {
		t.Fatalf("models=%d settings=%d, want 29/6", models, settings)
	}
}

func TestGoDoneClosesAfterFunc(t *testing.T) {
	release := make(chan struct{})
	d := goDone(func() { <-release })
	if waitDone(30*time.Millisecond, d) {
		t.Fatal("f 未结束时不应 done")
	}
	close(release)
	if !waitDone(time.Second, d) {
		t.Fatal("f 结束后应 done")
	}
}

// 停 HTTP、Drain、worker 三者并行：总耗时约等于最慢的一个，而不是相加。
func TestShutdownAndWaitRunsInParallel(t *testing.T) {
	step := 150 * time.Millisecond
	worker := goDone(func() { time.Sleep(step) })
	start := time.Now()
	ok := shutdownAndWait(2*time.Second,
		func(context.Context) error { time.Sleep(step); return nil },
		func() { time.Sleep(step) },
		worker)
	if !ok {
		t.Fatal("应在期限内全部结束")
	}
	if el := time.Since(start); el > 2*step+50*time.Millisecond {
		t.Fatalf("应并行等待，耗时 %v", el)
	}
}

// 任何一项卡住时，整体不超过 timeout，且 stopHTTP 拿到的 ctx 带同一个截止时间。
func TestShutdownAndWaitBoundedByTimeout(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	var gotDeadline bool
	start := time.Now()
	ok := shutdownAndWait(200*time.Millisecond,
		func(ctx context.Context) error { _, gotDeadline = ctx.Deadline(); <-ctx.Done(); return ctx.Err() },
		func() { <-block })
	if ok {
		t.Fatal("drain 卡住时应返回 false")
	}
	if el := time.Since(start); el > 400*time.Millisecond {
		t.Fatalf("总等待应受 timeout 约束，耗时 %v", el)
	}
	if !gotDeadline {
		t.Fatal("stopHTTP 的 ctx 应带截止时间")
	}
}
