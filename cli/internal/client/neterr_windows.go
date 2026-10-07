//go:build windows

package client

import (
	"errors"
	"syscall"
)

// Windows 上 syscall.ECONNREFUSED 是 Go 自造的值，与 Winsock 实际返回的 WSAECONNREFUSED 不相等。
const (
	wsaEACCES       syscall.Errno = 10013
	wsaECONNREFUSED syscall.Errno = 10061
)

func isConnRefused(err error) bool {
	return errors.Is(err, wsaECONNREFUSED) || errors.Is(err, syscall.ECONNREFUSED)
}

func isPermDenied(err error) bool {
	return errors.Is(err, wsaEACCES) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES)
}
