package main

import (
	"bufio"
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
func startSidecar(t *testing.T, bin, dataDir string) (*exec.Cmd, io.WriteCloser, string) {
	t.Helper()
	cmd := exec.Command(bin, "--data-dir", dataDir)
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
func TestExitsWhenStdinCloses(t *testing.T) {
	bin := buildSidecar(t)
	cmd, stdin, _ := startSidecar(t, bin, t.TempDir())
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
