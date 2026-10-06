package skillruntime_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"

	"github.com/james/huabu_studio/apps/api/internal/skillbundle"
	"github.com/james/huabu_studio/apps/api/internal/skillruntime"
)

// bundledPython 用 Task 1 准备的内置 Python；没准备时跳过（CI / 未运行 fetch-python.sh 的 worktree）。
func bundledPython(t *testing.T) string {
	t.Helper()
	home := os.Getenv(skillruntime.PythonHomeEnv)
	if home == "" {
		home = filepath.Join("..", "..", "..", "src-tauri", "resources", "python")
	}
	p := skillruntime.PythonInterpreter(home)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("未找到内置 Python（%s）：先运行 scripts/fetch-python.sh", p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// newBundledRun 在带空格和中文的数据目录里释放内置 Skill，并为指定 Skill 建一个 Run 工作区。
func newBundledRun(t *testing.T, skill string) (*skillruntime.RestrictedShell, string) {
	t.Helper()
	python := bundledPython(t)
	base := filepath.Join(t.TempDir(), "Application Support", "张三")
	skillsDir, err := skillbundle.Extract(filepath.Join(base, "runtime", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(skillsDir, skill)
	ws, err := skillruntime.NewRunWorkspace(filepath.Join(base, "runtime", "skill-runs"))
	if err != nil {
		t.Fatal(err)
	}
	runDir, err := ws.Create(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	shell, err := skillruntime.NewRestrictedShell(runDir, skillDir, skillruntime.RestrictedShellConfig{Python: python})
	if err != nil {
		t.Fatal(err)
	}
	return shell, runDir
}

func execute(t *testing.T, shell *skillruntime.RestrictedShell, command string) (int, string) {
	t.Helper()
	resp, err := shell.Execute(context.Background(), &filesystem.ExecuteRequest{Command: command})
	if err != nil {
		t.Fatalf("%q: %v", command, err)
	}
	return *resp.ExitCode, resp.Output
}

func TestBundledPythonRunsRealValidator(t *testing.T) {
	shell, runDir := newBundledRun(t, "real-person-short-drama-v3")
	if err := os.WriteFile(filepath.Join(runDir, "candidate.json"), []byte(`{"storyboard":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := execute(t, shell, "python3 scripts/validate_storyboard.py candidate.json --duration-sec 10")
	if code != 1 || !strings.Contains(out, "ERROR: 顶层 storyboard 不得为空") {
		t.Fatalf("真实校验脚本应报出中文错误并以 1 退出: exit=%d\n%s", code, out)
	}
}

func TestBundledPythonEmbedTextSatisfiesAdaptedValidator(t *testing.T) {
	shell, runDir := newBundledRun(t, "novel-real-person-short-drama-v1")
	source := "安岁推开门。她说：“你回来了？”\n雨还在下。"
	if err := os.WriteFile(filepath.Join(runDir, "source.txt"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := `{"sourceText":"安岁推门。","sourceSentences":[],"scriptLines":[],"scriptText":"x"}`
	if err := os.WriteFile(filepath.Join(runDir, "adapted_script.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	const notVerbatim = "sourceText 必须从 source.txt 原样注入"
	_, before := execute(t, shell, "python3 scripts/validate_adapted_script.py adapted_script.json --source source.txt")
	if !strings.Contains(before, notVerbatim) {
		t.Fatalf("对照组：手抄的 sourceText 应被校验器指出\n%s", before)
	}
	if code, out := execute(t, shell, "embed-text adapted_script.json sourceText source.txt"); code != 0 {
		t.Fatalf("embed-text 失败: %s", out)
	}
	_, after := execute(t, shell, "python3 scripts/validate_adapted_script.py adapted_script.json --source source.txt")
	if strings.Contains(after, notVerbatim) {
		t.Fatalf("embed-text 之后 sourceText 应与 source.txt 一致\n%s", after)
	}
}
