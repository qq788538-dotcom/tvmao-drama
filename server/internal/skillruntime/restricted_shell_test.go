package skillruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/components/model"
)

// testPython 单元测试用本机 python3；内置 Python 的真实运行在 Task 8 的 bundled_python_test.go。
func testPython(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("本机没有 python3")
	return ""
}

const echoArgsScript = `import os, sys
print("ARGS=" + "|".join(sys.argv[1:]))
print("CWD=" + os.getcwd())
print("HOME=" + os.environ.get("HOME", os.environ.get("USERPROFILE", "")))
print("TMP=" + os.environ.get("TMPDIR", os.environ.get("TEMP", "")))
print("SECRET=" + os.environ.get("MIAOJU_SECRET", "<none>"))
print("中文校验通过")
`

type shellFixture struct {
	shell     *RestrictedShell
	workspace string
	skillDir  string
	outside   string
}

func newShellFixture(t *testing.T, cfg RestrictedShellConfig) shellFixture {
	t.Helper()
	base := t.TempDir()
	skillDir := filepath.Join(base, "skills", "demo")
	writeTestFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: demo\ndescription: demo\n---\n")
	writeTestFile(t, filepath.Join(skillDir, "scripts", "echo_args.py"), echoArgsScript)
	writeTestFile(t, filepath.Join(skillDir, "scripts", "exit3.py"), "import sys\nprint('bye')\nsys.exit(3)\n")
	writeTestFile(t, filepath.Join(skillDir, "scripts", "sleep.py"), "import time\ntime.sleep(5)\n")
	writeTestFile(t, filepath.Join(skillDir, "scripts", "flood.py"), "import sys\nsys.stdout.write('x' * (2 * 1024 * 1024))\n")
	outside := filepath.Join(base, "outside")
	writeTestFile(t, filepath.Join(outside, "secret.json"), `{"k":"v"}`)
	ws, err := NewRunWorkspace(filepath.Join(base, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	runDir, err := ws.Create(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Python == "" {
		cfg.Python = testPython(t)
	}
	shell, err := NewRestrictedShell(runDir, skillDir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return shellFixture{shell: shell, workspace: shell.workspace, skillDir: shell.skillDir, outside: outside}
}

func run(t *testing.T, s *RestrictedShell, command string) *filesystem.ExecuteResponse {
	t.Helper()
	resp, err := s.Execute(context.Background(), &filesystem.ExecuteRequest{Command: command})
	if err != nil {
		t.Fatalf("%q: Execute 不应返回 error: %v", command, err)
	}
	return resp
}

func exitCode(resp *filesystem.ExecuteResponse) int {
	if resp.ExitCode == nil {
		return -1
	}
	return *resp.ExitCode
}

func TestRestrictedShellRunsSkillScriptWithWorkspaceCwd(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	writeTestFile(t, filepath.Join(f.workspace, "candidate.json"), "{}")
	resp := run(t, f.shell, "python3 scripts/echo_args.py candidate.json --duration-sec 10 --source=source.txt 2>&1")
	if exitCode(resp) != 0 {
		t.Fatalf("exit=%d output=%s", exitCode(resp), resp.Output)
	}
	wantArgs := "ARGS=candidate.json|--duration-sec|10|--source=source.txt"
	if !strings.Contains(resp.Output, wantArgs) || !strings.Contains(resp.Output, "CWD="+f.workspace) {
		t.Fatalf("参数或 cwd 不对: %s", resp.Output)
	}
	if !strings.Contains(resp.Output, "中文校验通过") {
		t.Fatalf("中文输出应原样返回（-X utf8）: %q", resp.Output)
	}
}

func TestRestrictedShellScrubsEnvironment(t *testing.T) {
	t.Setenv("MIAOJU_SECRET", "leak")
	t.Setenv("PYTHONPATH", "/somewhere")
	f := newShellFixture(t, RestrictedShellConfig{})
	resp := run(t, f.shell, "python3 scripts/echo_args.py")
	if !strings.Contains(resp.Output, "SECRET=<none>") {
		t.Fatalf("父进程环境变量泄露给了脚本: %s", resp.Output)
	}
	if !strings.Contains(resp.Output, "HOME="+f.workspace) || !strings.Contains(resp.Output, "TMP="+filepath.Join(f.workspace, ".tmp")) {
		t.Fatalf("HOME/TMP 应指向工作区: %s", resp.Output)
	}
}

func TestRestrictedShellRunsOriginalNotWorkspaceCopy(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	// 模型用 write_file 改写了工作区里的脚本副本，又新建了一个脚本。
	writeTestFile(t, filepath.Join(f.workspace, "scripts", "echo_args.py"), "print('TAMPERED')\n")
	writeTestFile(t, filepath.Join(f.workspace, "scripts", "evil.py"), "print('EVIL')\n")

	resp := run(t, f.shell, "python3 scripts/echo_args.py")
	if strings.Contains(resp.Output, "TAMPERED") || !strings.Contains(resp.Output, "ARGS=") {
		t.Fatalf("应执行原始 Skill 脚本，而不是工作区副本: %s", resp.Output)
	}
	resp = run(t, f.shell, "python3 scripts/evil.py")
	if exitCode(resp) != exitRejected || strings.Contains(resp.Output, "EVIL") || !strings.Contains(resp.Output, "Skill 目录里没有脚本") {
		t.Fatalf("工作区里新建的脚本必须拒绝: exit=%d %s", exitCode(resp), resp.Output)
	}
}

func TestRestrictedShellRejectsDisallowedCommandsWithExit126(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	for _, cmd := range []string{
		`python3 -c "import os; print(os.listdir('/'))"`,
		"cd scripts && python3 echo_args.py",
		"python3 scripts/echo_args.py | head -5",
		"python3 scripts/echo_args.py candidate.json --duration-sec <DURATION_SEC>",
		"ls -la",
		"rm -rf .",
	} {
		resp := run(t, f.shell, cmd)
		if exitCode(resp) != exitRejected {
			t.Errorf("%q: exit=%d，应为 %d", cmd, exitCode(resp), exitRejected)
		}
		if !strings.Contains(resp.Output, "命令被拒绝") || !strings.Contains(resp.Output, "embed-text <JSON 文件>") {
			t.Errorf("%q: 拒绝消息应说明原因和可用写法: %s", cmd, resp.Output)
		}
	}
}

func TestRestrictedShellRejectsPathEscapes(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	cases := []string{
		"python3 scripts/echo_args.py ../outside/secret.json",
		"python3 scripts/echo_args.py " + filepath.ToSlash(filepath.Join(f.outside, "secret.json")),
		"python3 scripts/echo_args.py --source=../../x.txt",
		"python3 ../demo/scripts/echo_args.py",
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(f.outside, "secret.json"), filepath.Join(f.workspace, "link.json")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(f.outside, filepath.Join(f.workspace, "linkdir")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(f.outside, "secret.json"), filepath.Join(f.skillDir, "scripts", "link.py")); err != nil {
			t.Fatal(err)
		}
		cases = append(cases,
			"python3 scripts/echo_args.py link.json",
			"python3 scripts/echo_args.py linkdir/secret.json",
			"python3 scripts/echo_args.py linkdir/new.json",
			"python3 scripts/link.py",
		)
	}
	for _, cmd := range cases {
		resp := run(t, f.shell, cmd)
		if exitCode(resp) != exitRejected || strings.Contains(resp.Output, "ARGS=") {
			t.Errorf("%q: 越界路径必须拒绝且不执行脚本: exit=%d %s", cmd, exitCode(resp), resp.Output)
		}
	}
}

func TestRestrictedShellReportsNonZeroExit(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	resp := run(t, f.shell, "python3 scripts/exit3.py")
	if exitCode(resp) != 3 || !strings.Contains(resp.Output, "bye") {
		t.Fatalf("exit=%d output=%s", exitCode(resp), resp.Output)
	}
}

func TestRestrictedShellTimeout(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{Timeout: 300 * time.Millisecond})
	start := time.Now()
	resp := run(t, f.shell, "python3 scripts/sleep.py")
	if exitCode(resp) != exitTimeout || !strings.Contains(resp.Output, "被终止") {
		t.Fatalf("exit=%d output=%s", exitCode(resp), resp.Output)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("超时后应尽快返回，用了 %s", time.Since(start))
	}
}

func TestRestrictedShellCapsOutput(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	resp := run(t, f.shell, "python3 scripts/flood.py")
	if !resp.Truncated || len(resp.Output) > maxShellOutputBytes {
		t.Fatalf("truncated=%v len=%d", resp.Truncated, len(resp.Output))
	}
}

func TestNewRestrictedShellRequiresPython(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewRestrictedShell(dir, dir, RestrictedShellConfig{}); err == nil || !strings.Contains(err.Error(), "内置 Python") {
		t.Fatalf("缺少解释器应报错: %v", err)
	}
	if _, err := NewRestrictedShell(dir, dir, RestrictedShellConfig{Python: filepath.Join(dir, "nope")}); err == nil {
		t.Fatal("解释器不存在应报错")
	}
}

func orderedKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	fields, err := decodeOrderedObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, f := range fields {
		keys = append(keys, f.Key)
	}
	return keys
}

func TestEmbedTextCopiesSourceVerbatimAndKeepsKeyOrder(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	source := strings.Repeat("安岁推开门，说：“你回来了？”\\n<b>&</b>\t", 200) + "\n最后一行没有换行"
	writeTestFile(t, filepath.Join(f.workspace, "source.txt"), source)
	writeTestFile(t, filepath.Join(f.workspace, "adapted_script.json"),
		`{"sourceText":"手抄的摘要","sourceSentences":[{"id":"S001"}],"scriptLines":[],"scriptText":"x"}`)

	resp := run(t, f.shell, "embed-text adapted_script.json sourceText source.txt")
	if exitCode(resp) != 0 || !strings.HasPrefix(resp.Output, "OK:") {
		t.Fatalf("exit=%d output=%s", exitCode(resp), resp.Output)
	}
	raw, err := os.ReadFile(filepath.Join(f.workspace, "adapted_script.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		SourceText      string            `json:"sourceText"`
		SourceSentences []json.RawMessage `json:"sourceSentences"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SourceText != source {
		t.Fatal("sourceText 必须与 source.txt 逐字一致")
	}
	if len(doc.SourceSentences) != 1 {
		t.Fatal("其他字段必须保留")
	}
	if got := strings.Join(orderedKeys(t, raw), ","); got != "sourceText,sourceSentences,scriptLines,scriptText" {
		t.Fatalf("字段顺序必须保持: %s", got)
	}

	// 字段不存在时追加到末尾；JSON 文件不存在时新建。
	resp = run(t, f.shell, "embed-text new.json sourceText source.txt")
	if exitCode(resp) != 0 {
		t.Fatalf("新建 JSON 失败: %s", resp.Output)
	}
	writeTestFile(t, filepath.Join(f.workspace, "plan.json"), `{"b":1,"a":2}`)
	run(t, f.shell, "embed-text plan.json zz source.txt")
	raw, _ = os.ReadFile(filepath.Join(f.workspace, "plan.json"))
	if got := strings.Join(orderedKeys(t, raw), ","); got != "b,a,zz" {
		t.Fatalf("新字段应追加在末尾: %s", got)
	}
	if bytes.Contains(raw, []byte(`\u003c`)) {
		t.Fatal("不应转义 < > &，保持原文可读")
	}
}

func TestEmbedTextRejectsBadInput(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	writeTestFile(t, filepath.Join(f.workspace, "source.txt"), "原文")
	writeTestFile(t, filepath.Join(f.workspace, "array.json"), `[1,2]`)
	for _, cmd := range []string{
		"embed-text array.json sourceText source.txt",
		"embed-text a.json source-text source.txt",
		"embed-text a.json sourceText missing.txt",
		"embed-text ../a.json sourceText source.txt",
		"embed-text a.json sourceText ../outside/secret.json",
	} {
		if resp := run(t, f.shell, cmd); exitCode(resp) != exitRejected {
			t.Errorf("%q: exit=%d output=%s", cmd, exitCode(resp), resp.Output)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.workspace), "a.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("越界写入不应发生")
	}
}

// Runtime 必须把原始 Skill 目录交给 RestrictedShell：模型发出的 python3 scripts/generate.py 能跑通。
func TestRuntimeWithRestrictedShellRunsSkillScript(t *testing.T) {
	python := testPython(t)
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "script-style", "SKILL.md"), "---\nname: script-style\ndescription: Script style\n---\nRun scripts/generate.py and use its result.\n")
	writeTestFile(t, filepath.Join(root, "script-style", "scripts", "generate.py"), "from pathlib import Path\nprint(Path(\"candidate.json\").read_text())\n")
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Snapshot(context.Background(), "script-style")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &shellModelState{}
	rt := NewRuntime(catalog, workspace, func(context.Context, ModelConfig) (model.ToolCallingChatModel, error) {
		return &shellScriptModel{state: state}, nil
	}, 10, RestrictedShellFactory(RestrictedShellConfig{Python: python}))
	result, err := rt.Run(context.Background(), RunRequest{Skill: snapshot, Model: ModelConfig{APIKey: "sk", Model: "test"}, Input: "run it"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "final from script" || !state.sawExecuteTool || !state.sawScriptOutput {
		t.Fatalf("result=%#v state=%#v", result, state)
	}
}
