package skillruntime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cloudwego/eino/adk/filesystem"
)

// TrustedShell 用本机 bash 直接执行命令、不做任何限制。只存在于 _test.go：
// runner 的单元测试靠它保持 hermetic，生产代码无法引用它（生产只能装配 RestrictedShell）。
type TrustedShell struct {
	root string
	bash string
	tmp  string
}

func TrustedShellFactory(workspace, _ string) (filesystem.Shell, error) {
	return NewTrustedShell(workspace)
}

func NewTrustedShell(root string) (*TrustedShell, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve trusted shell root: %w", err)
	}
	info, err := os.Stat(realRoot)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("trusted shell root is not a directory")
	}
	tmp := filepath.Join(realRoot, ".tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(tmp, 0o700); err != nil {
		return nil, err
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		return nil, errors.New("bash is required for TrustedShell tests")
	}
	return &TrustedShell{root: realRoot, bash: bash, tmp: tmp}, nil
}

func (s *TrustedShell) Execute(ctx context.Context, input *filesystem.ExecuteRequest) (*filesystem.ExecuteResponse, error) {
	if input == nil || input.Command == "" {
		return nil, errors.New("command is required")
	}
	cmd := exec.CommandContext(ctx, s.bash, "-lc", input.Command)
	cmd.Dir = s.root
	cmd.Env = append(os.Environ(), "SKILL_DIR="+s.root, "WORK_DIR="+s.root, "TMPDIR="+s.tmp, "PYTHONUNBUFFERED=1")
	output := &cappedBuffer{max: maxShellOutputBytes}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			if errors.Is(err, fs.ErrPermission) {
				return nil, fmt.Errorf("execute command: %w", err)
			}
			return nil, err
		}
		exitCode = exitErr.ExitCode()
	}
	return &filesystem.ExecuteResponse{Output: output.String(), ExitCode: &exitCode, Truncated: output.truncated}, nil
}

var _ filesystem.Shell = (*TrustedShell)(nil)
