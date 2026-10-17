//go:build !windows

package client

import (
	"errors"
	"syscall"
)

func isConnRefused(err error) bool { return errors.Is(err, syscall.ECONNREFUSED) }

// Codex 的 seatbelt 沙箱拦截网络时 connect 返回 EPERM（实测）；EACCES 一并视为拦截。
func isPermDenied(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES)
}
