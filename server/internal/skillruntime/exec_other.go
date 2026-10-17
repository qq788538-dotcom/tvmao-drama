//go:build !windows

package skillruntime

import "os/exec"

func configureChildProcess(*exec.Cmd) {}

func platformEnv() []string { return nil }
