//go:build windows

package mediaprobe

import (
	"os/exec"
	"syscall"
)

// sidecar 本身没有控制台；不加 CREATE_NO_WINDOW 的话，每次起 ffprobe.exe 都会闪出一个黑框。
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
