package skillruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// PythonHomeEnv 开发时可用它指向 src-tauri/resources/python。
const PythonHomeEnv = "MIAOJU_PYTHON_HOME"

// PythonInterpreter 返回 python-build-standalone 目录里的解释器路径。
func PythonInterpreter(home string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "python.exe")
	}
	return filepath.Join(home, "bin", "python3")
}

// LocatePython 依次尝试：--python-home → $MIAOJU_PYTHON_HOME → <sidecar>/../Resources/python（macOS .app）
// → <sidecar 同级>/python（Windows zip、cargo tauri dev）。返回解释器绝对路径。
func LocatePython(explicitHome string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}
	return locatePython(explicitHome, os.Getenv(PythonHomeEnv), exe)
}

func locatePython(explicitHome, envHome, exe string) (string, error) {
	var homes []string
	if explicitHome != "" {
		homes = append(homes, explicitHome)
	}
	if envHome != "" {
		homes = append(homes, envHome)
	}
	if exe != "" {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		homes = append(homes, filepath.Join(dir, "..", "Resources", "python"), filepath.Join(dir, "python"))
	}
	var tried []string
	for _, home := range homes {
		p := PythonInterpreter(home)
		tried = append(tried, p)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			abs, err := filepath.Abs(p)
			if err != nil {
				return "", err
			}
			return abs, nil
		}
	}
	return "", fmt.Errorf("找不到内置 Python 运行时（已尝试：%s）", strings.Join(tried, "、"))
}
