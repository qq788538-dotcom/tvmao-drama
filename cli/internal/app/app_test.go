package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/james/huabu_studio/apps/cli/internal/client"
	"github.com/james/huabu_studio/apps/cli/internal/endpoint"
	"github.com/james/huabu_studio/apps/cli/internal/exitcode"
	"github.com/james/huabu_studio/apps/cli/internal/workspace"
)

// harness 是一次命令执行的完整隔离环境：自己的 stdout/stderr、
// 自己的工作目录（项目绑定）、自己的假服务端。
type harness struct {
	// noEndpoint 为 true 时 run 不写 cli-endpoint.json（模拟应用未运行）。
	noEndpoint bool
	// endpointAppVersion 非空时写进地址文件的 appVersion（默认用 version）。
	endpointAppVersion string
	t                  *testing.T
	stdout, stderr     bytes.Buffer
	// stdin 默认空；需要喂 stdin 的用例（node move --file -）在 run 前赋值。
	stdin   io.Reader
	workDir string
	server  *httptest.Server
	// endpointDir 存放 cli-endpoint.json；run 时按 server 端口与 h.version 写入。
	endpointDir string
	version     string
}

// harnessCLIToken 是 harness 写进地址文件的 token，ping 校验它。
const harnessCLIToken = "cli-test-token"

func newHarness(t *testing.T, h http.HandlerFunc) *harness {
	t.Helper()
	hs := &harness{workDir: t.TempDir(), endpointDir: t.TempDir(), version: "dev"}
	// 包一层响应 /api/v1/cli/ping，其余请求交给 h。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/cli/ping" {
			if r.Header.Get("Authorization") != "Bearer "+harnessCLIToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"app": "miaoju-desktop", "version": hs.version})
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	hs.server = srv
	hs.t = t
	return hs
}

// writeEndpoint 按当前 server 端口与 h.version 写 cli-endpoint.json。
func (h *harness) writeEndpoint() {
	h.t.Helper()
	u, err := url.Parse(h.server.URL)
	if err != nil {
		h.t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		h.t.Fatal(err)
	}
	appVer := h.version
	if h.endpointAppVersion != "" {
		appVer = h.endpointAppVersion
	}
	blob, _ := json.Marshal(endpoint.Info{Version: 1, Port: port, Token: harnessCLIToken, AppVersion: appVer})
	if err := os.WriteFile(filepath.Join(h.endpointDir, endpoint.FileName), blob, 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) run(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	if !h.noEndpoint {
		h.writeEndpoint()
	}
	root := NewRootCommand(Dependencies{
		Stdin:       h.stdinOrEmpty(),
		Stdout:      &h.stdout,
		Stderr:      &h.stderr,
		Version:     h.version,
		WorkDir:     h.workDir,
		EndpointDir: h.endpointDir,
	})
	return Execute(root, args, &h.stderr)
}

func jsonHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}
}

// ---- 结构性约束 ----

// 两个 root 实例必须各写各的 stdout —— 这是「无包级可变状态」的可观测证据。
func TestRootCommandHasNoPackageGlobals(t *testing.T) {
	h1 := newHarness(t, jsonHandler(`{}`))
	h2 := newHarness(t, jsonHandler(`{}`))
	h1.version = "1.0.0"
	h2.version = "2.0.0"

	if code := h1.run("version"); code != exitcode.OK {
		t.Fatalf("h1 = %d", code)
	}
	if code := h2.run("version"); code != exitcode.OK {
		t.Fatalf("h2 = %d", code)
	}
	if !strings.Contains(h1.stdout.String(), "1.0.0") {
		t.Fatalf("h1 stdout = %q", h1.stdout.String())
	}
	if !strings.Contains(h2.stdout.String(), "2.0.0") {
		t.Fatalf("h2 stdout = %q", h2.stdout.String())
	}
	if strings.Contains(h1.stdout.String(), "2.0.0") {
		t.Fatal("两个实例的输出串了")
	}
}

// 参数错误才打 usage；业务错误打整页 usage 会淹没真正的信息。
func TestFlagErrorPrintsUsageAndExits2(t *testing.T) {
	h := newHarness(t, jsonHandler(`{}`))
	code := h.run("--不存在的flag")
	if code != exitcode.Usage {
		t.Fatalf("code = %d, want %d", code, exitcode.Usage)
	}
	if !strings.Contains(h.stderr.String(), "用法") {
		t.Fatalf("stderr 应含用法提示: %q", h.stderr.String())
	}
}

func TestBusinessErrorDoesNotPrintUsage(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"PROJECT_NOT_FOUND","message":"项目不存在","requestId":"req-1"}}`))
	})

	code := h.run("project", "get", "42")
	if code != exitcode.NotFound {
		t.Fatalf("code = %d, want %d", code, exitcode.NotFound)
	}
	if strings.Contains(h.stderr.String(), "用法") || strings.Contains(h.stderr.String(), "Usage:") {
		t.Fatalf("业务错误不该打 usage: %q", h.stderr.String())
	}
	// request-id 要给出来，用户贴它就能定位服务端日志。
	if !strings.Contains(h.stderr.String(), "req-1") {
		t.Fatalf("stderr 应含 request-id: %q", h.stderr.String())
	}
}

// stdout 必须是合法 JSON —— Agent 直接 | jq，不该从日志里抠。
func TestJSONStdoutHasNoProgressText(t *testing.T) {
	h := newHarness(t, jsonHandler(`{"data":[{"id":1,"name":"画布A"}]}`))

	if code := h.run("project", "list"); code != exitcode.OK {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout 不是合法 JSON: %q", h.stdout.String())
	}
	if len(out) != 1 || out[0]["name"] != "画布A" {
		t.Fatalf("out = %+v", out)
	}
}

func TestTableOutputGoesToStdout(t *testing.T) {
	h := newHarness(t, jsonHandler(`{"data":[{"id":1,"name":"画布A"},{"id":2,"name":"画布B"}]}`))

	if code := h.run("project", "list", "-o", "table"); code != exitcode.OK {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}
	out := h.stdout.String()
	if !strings.Contains(out, "画布A") || !strings.Contains(out, "画布B") {
		t.Fatalf("table 输出 = %q", out)
	}
}

func TestNDJSONOutputsOnePerLine(t *testing.T) {
	h := newHarness(t, jsonHandler(`{"data":[{"id":1},{"id":2},{"id":3}]}`))

	h.run("project", "list", "-o", "ndjson")
	lines := strings.Split(strings.TrimSpace(h.stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("应有 3 行, got %d: %q", len(lines), h.stdout.String())
	}
	for _, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("第 %q 行不是合法 JSON", line)
		}
	}
}

func TestInvalidOutputFormatExits2(t *testing.T) {
	h := newHarness(t, jsonHandler(`{}`))
	if code := h.run("project", "list", "-o", "yaml"); code != exitcode.Usage {
		t.Fatalf("code = %d", code)
	}
}

// ---- 项目绑定 ----

func TestProjectUseWritesBindingAndSubsequentCommandsUseIt(t *testing.T) {
	var lastPath string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		_, _ = w.Write([]byte(`{"id":42,"name":"画布","data":[]}`))
	})

	if code := h.run("project", "use", "42"); code != exitcode.OK {
		t.Fatalf("use = %d stderr=%s", code, h.stderr.String())
	}
	b, err := workspace.LoadProjectBinding(h.workDir)
	if err != nil || b == nil || b.ProjectID != 42 {
		t.Fatalf("binding = %+v %v", b, err)
	}

	if code := h.run("node", "list"); code != exitcode.OK {
		t.Fatalf("node list = %d stderr=%s", code, h.stderr.String())
	}
	if !strings.Contains(lastPath, "/projects/42/nodes") {
		t.Fatalf("未使用绑定项目: %s", lastPath)
	}
}

// 无绑定时报错而不是猜——猜错会往别人的画布里写节点。
func TestNoProjectBindingExits2(t *testing.T) {
	h := newHarness(t, jsonHandler(`{"data":[]}`))
	if code := h.run("node", "list"); code != exitcode.Usage {
		t.Fatalf("code = %d, want %d", code, exitcode.Usage)
	}
}

func TestProjectFlagOverridesBinding(t *testing.T) {
	var lastPath string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	_ = workspace.SaveProjectBinding(h.workDir, workspace.ProjectBinding{ProjectID: 42})

	h.run("node", "list", "--project", "7")
	if !strings.Contains(lastPath, "/projects/7/") {
		t.Fatalf("--project 未生效: %s", lastPath)
	}
}

// 非交互环境执行破坏性操作必须显式 --yes，且不能挂起等输入。
func TestDeleteRequiresYesInNonTTY(t *testing.T) {
	h := newHarness(t, jsonHandler(`{}`))
	if code := h.run("project", "delete", "42"); code != exitcode.Usage {
		t.Fatalf("code = %d, want %d", code, exitcode.Usage)
	}
}

func (h *harness) stdinOrEmpty() io.Reader {
	if h.stdin == nil {
		return strings.NewReader("")
	}
	return h.stdin
}

func TestAppNotRunningExits8(t *testing.T) {
	h := newHarness(t, http.NotFound)
	h.noEndpoint = true
	code := h.run("project", "list")
	if code != exitcode.AppNotRunning || !strings.Contains(h.stderr.String(), "喵剧未运行，请先打开应用") {
		t.Fatalf("code=%d stderr=%q", code, h.stderr.String())
	}
}

func TestVersionWorksWithoutApp(t *testing.T) {
	h := newHarness(t, http.NotFound)
	h.noEndpoint = true
	if code := h.run("version"); code != 0 {
		t.Fatalf("version 不应依赖应用是否打开，code=%d", code)
	}
}

func TestVersionMismatchWarnsOnStderr(t *testing.T) {
	h := newHarness(t, jsonHandler(`{"data":[]}`))
	h.version = "1.0.0"
	h.endpointAppVersion = "9.9.9"
	code := h.run("project", "list")
	if code != 0 || !strings.Contains(h.stderr.String(), "9.9.9") || strings.Contains(h.stdout.String(), "9.9.9") {
		t.Fatalf("code=%d stdout=%q stderr=%q；警告只能走 stderr", code, h.stdout.String(), h.stderr.String())
	}
}

func TestReportErrorMapsSandboxBlocked(t *testing.T) {
	var buf bytes.Buffer
	code := reportError(&cobra.Command{Use: "miaoju"}, &client.SandboxBlockedError{Err: errors.New("x")}, &buf)
	if code != exitcode.SandboxBlocked || !strings.Contains(buf.String(), "网络被 AI 助手的沙箱拦截，请允许本命令访问网络后重试") {
		t.Fatalf("code=%d out=%q", code, buf.String())
	}
}

// 401 连续两次后的 AppNotRunningError 里包着 APIError，必须先判 AppNotRunning。
func TestReportErrorAppNotRunningWrappingAPIError(t *testing.T) {
	var buf bytes.Buffer
	err := &client.AppNotRunningError{Err: &client.APIError{Status: 401, Message: "bad"}}
	code := reportError(&cobra.Command{Use: "miaoju"}, err, &buf)
	if code != exitcode.AppNotRunning || !strings.Contains(buf.String(), "喵剧未运行，请先打开应用") {
		t.Fatalf("code=%d out=%q", code, buf.String())
	}
}
