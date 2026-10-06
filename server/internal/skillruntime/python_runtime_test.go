package skillruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makePythonHome(t *testing.T, home string) string {
	t.Helper()
	p := PythonInterpreter(home)
	writeTestFile(t, p, "fake")
	return p
}

func TestLocatePythonOrder(t *testing.T) {
	base := t.TempDir()
	explicit := filepath.Join(base, "explicit")
	env := filepath.Join(base, "env")
	exeDir := filepath.Join(base, "喵剧AI.app", "Contents", "MacOS")
	exe := filepath.Join(exeDir, "sidecar")
	writeTestFile(t, exe, "bin")

	wantExplicit := makePythonHome(t, explicit)
	wantEnv := makePythonHome(t, env)
	wantMac := makePythonHome(t, filepath.Join(exeDir, "..", "Resources", "python"))

	if got, err := locatePython(explicit, env, exe); err != nil || !sameFile(t, got, wantExplicit) {
		t.Fatalf("显式参数优先: got %q %v", got, err)
	}
	if got, err := locatePython("", env, exe); err != nil || !sameFile(t, got, wantEnv) {
		t.Fatalf("其次环境变量: got %q %v", got, err)
	}
	if got, err := locatePython("", "", exe); err != nil || !sameFile(t, got, wantMac) {
		t.Fatalf("再次 .app 的 Resources: got %q %v", got, err)
	}

	winDir := filepath.Join(base, "miaoju-windows-x64")
	winExe := filepath.Join(winDir, "sidecar.exe")
	writeTestFile(t, winExe, "bin")
	wantWin := makePythonHome(t, filepath.Join(winDir, "python"))
	if got, err := locatePython("", "", winExe); err != nil || !sameFile(t, got, wantWin) {
		t.Fatalf("最后 exe 同级 python/: got %q %v", got, err)
	}
}

// sameFile 按文件身份比较：macOS 的 t.TempDir() 在 /var 下，EvalSymlinks 后变成 /private/var，字符串不相等。
func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ia, ib)
}

func TestLocatePythonReportsCandidates(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "sidecar")
	writeTestFile(t, exe, "bin")
	_, err := locatePython("", "", exe)
	if err == nil || !strings.Contains(err.Error(), "找不到内置 Python") || !strings.Contains(err.Error(), "Resources") {
		t.Fatalf("应报出尝试过的路径: %v", err)
	}
}

func TestPythonInterpreterLayout(t *testing.T) {
	got := PythonInterpreter("H")
	if !(got == filepath.Join("H", "python.exe") || got == filepath.Join("H", "bin", "python3")) {
		t.Fatalf("unexpected interpreter path %q", got)
	}
	_ = os.Getenv(PythonHomeEnv)
}
