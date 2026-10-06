//go:build !darwin && !windows

package reveal

import (
	"os/exec"
	"path/filepath"
)

// Linux 不是目标平台（spec D6），只为能编译：打开所在目录，不选中文件。
func inFileManager(path string) error {
	return exec.Command("xdg-open", filepath.Dir(path)).Start()
}
