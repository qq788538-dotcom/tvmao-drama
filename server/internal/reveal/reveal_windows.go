package reveal

import (
	"os/exec"
	"syscall"
)

func inFileManager(path string) error {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: windowsCmdLine(path)}
	// explorer 成功时也常返回退出码 1，只看能不能启动起来。
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
