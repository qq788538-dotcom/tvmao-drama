//go:build !windows

package client

import (
	"errors"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
)

func dialErr(errno syscall.Errno) error {
	return &url.Error{Op: "Get", URL: "http://127.0.0.1:1/x",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}}
}

func TestClassifyNetErr(t *testing.T) {
	var anr *AppNotRunningError
	var sb *SandboxBlockedError
	var ne *NetworkError
	if err := classifyNetErr(dialErr(syscall.ECONNREFUSED)); !errors.As(err, &anr) {
		t.Fatalf("ECONNREFUSED → %T", err)
	}
	if err := classifyNetErr(dialErr(syscall.EPERM)); !errors.As(err, &sb) {
		t.Fatalf("EPERM → %T", err)
	}
	if err := classifyNetErr(dialErr(syscall.EACCES)); !errors.As(err, &sb) {
		t.Fatalf("EACCES → %T", err)
	}
	if err := classifyNetErr(errors.New("timeout")); !errors.As(err, &ne) {
		t.Fatalf("其他 → %T", err)
	}
}
