package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/cli/internal/client"
	"github.com/james/huabu_studio/apps/cli/internal/exitcode"
	"github.com/james/huabu_studio/apps/cli/internal/workspace"
)

// harness 是一次命令执行的完整隔离环境：自己的 stdout/stderr、
// 自己的工作目录（项目绑定）、自己的假服务端。
type harness struct {
	stdout, stderr bytes.Buffer
	// stdin 默认空；需要喂 stdin 的用例（node move --file -）在 run 前赋值。
	stdin   io.Reader
	workDir string
	server  *httptest.Server
	env     map[string]string
	version string
}

func newHarness(t *testing.T, h http.HandlerFunc) *harness {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &harness{
		workDir: t.TempDir(), server: srv,
		env: map[string]string{}, version: "dev",
	}
}

func (h *harness) run(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	root := NewRootCommand(Dependencies{
		ClientFactory: func() *client.Client {
			return client.New(client.Options{Server: h.server.URL, Token: "test", Version: h.version})
		},
		Stdin:   h.stdinOrEmpty(),
		Stdout:  &h.stdout,
		Stderr:  &h.stderr,
		Version: h.version,
		WorkDir: h.workDir,
		Env:     func(k string) string { return h.env[k] },
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
