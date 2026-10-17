//go:build windows

package skillruntime

import (
	"os"
	"os/exec"
	"syscall"
)

// sidecar 本身没有控制台；不加 CREATE_NO_WINDOW 的话，每次起 python.exe 都会弹出一个黑框。
const createNoWindow = 0x08000000

func configureChildProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// Windows 上 Python 初始化随机数需要 SYSTEMROOT，缺了会直接崩溃。
func platformEnv() []string {
	root := os.Getenv("SYSTEMROOT")
	if root == "" {
		root = `C:\Windows`
	}
	return []string{"SYSTEMROOT=" + root}
}
