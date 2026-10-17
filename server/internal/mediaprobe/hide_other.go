//go:build !windows

package mediaprobe

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
