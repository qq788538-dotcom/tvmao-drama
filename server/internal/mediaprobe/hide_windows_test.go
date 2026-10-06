//go:build windows

package mediaprobe

import (
	"os/exec"
	"testing"
)

func TestHideWindowSetsCreateNoWindow(t *testing.T) {
	cmd := exec.Command("ffprobe")
	hideWindow(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 || !cmd.SysProcAttr.HideWindow {
		t.Fatalf("ffprobe 子进程必须带 CREATE_NO_WINDOW，got %+v", cmd.SysProcAttr)
	}
}
