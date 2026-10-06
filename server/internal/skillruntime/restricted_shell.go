package skillruntime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
)

// 返回码约定：126 = 命令不在白名单（与 sh 的“无法执行”一致），124 = 超时（与 timeout(1) 一致）。
const (
	exitRejected = 126
	exitTimeout  = 124
)

const restrictedShellUsage = `execute 不是 bash，只支持两种命令（不支持管道、重定向、&&、;、变量、通配符和 python -c）：
  python3 scripts/<脚本名>.py [参数…]
      只能运行当前 Skill 自带的脚本；文件参数必须是工作区内的相对路径，例如：
      python3 scripts/validate_storyboard.py candidate.json --duration-sec 10
  embed-text <JSON 文件> <字段名> <文本文件>
      把文本文件原样写入 JSON 顶层字段（保持其他字段与顺序），例如：
      embed-text adapted_script.json sourceText source.txt
查看、写入文件请用文件工具（ls / read_file / write_file / edit_file）。`

type RestrictedShellConfig struct {
	Python  string        // 内置解释器绝对路径（LocatePython 的结果）
	Timeout time.Duration // 单条命令墙钟上限，<=0 用 30s
}

// RestrictedShell 是桌面端唯一的 Skill execute 实现（X6）：不经过任何 shell，
// 只用内置 Python 执行原始 Skill 目录里的脚本，文件参数限定在本次 Run 工作区内，环境从零构造。
type RestrictedShell struct {
	workspace string // EvalSymlinks 后的 Run 工作区
	skillDir  string // EvalSymlinks 后的原始 Skill 目录
	tmp       string
	python    string
	timeout   time.Duration
}

func NewRestrictedShell(workspace, skillDir string, cfg RestrictedShellConfig) (*RestrictedShell, error) {
	if strings.TrimSpace(cfg.Python) == "" {
		return nil, errors.New("内置 Python 运行时缺失，无法执行 Skill 脚本")
	}
	python, err := filepath.Abs(cfg.Python)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(python); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("内置 Python 运行时不可用（%s）: %v", python, err)
	}
	ws, err := realDirectory(workspace)
	if err != nil {
		return nil, fmt.Errorf("Skill Run 工作区: %w", err)
	}
	sd, err := realDirectory(skillDir)
	if err != nil {
		return nil, fmt.Errorf("Skill 目录: %w", err)
	}
	tmp := filepath.Join(ws, ".tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(tmp, 0o700); err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultShellTimeout
	}
	return &RestrictedShell{workspace: ws, skillDir: sd, tmp: tmp, python: python, timeout: timeout}, nil
}

// RestrictedShellFactory 解释器缺失时不在启动期报错，而是每次 Run 造 shell 时报错，任务以可读原因失败。
func RestrictedShellFactory(cfg RestrictedShellConfig) ShellFactory {
	return func(workspace, skillDir string) (filesystem.Shell, error) {
		return NewRestrictedShell(workspace, skillDir, cfg)
	}
}

func realDirectory(p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s 不是目录", real)
	}
	return real, nil
}

func (s *RestrictedShell) Execute(ctx context.Context, input *filesystem.ExecuteRequest) (*filesystem.ExecuteResponse, error) {
	if input == nil || strings.TrimSpace(input.Command) == "" {
		return nil, errors.New("command is required")
	}
	resp, err := s.execute(ctx, input.Command)
	if errors.Is(err, ErrCommandRejected) {
		code := exitRejected
		return &filesystem.ExecuteResponse{Output: "命令被拒绝：" + err.Error() + "\n\n" + restrictedShellUsage + "\n", ExitCode: &code}, nil
	}
	return resp, err
}

func (s *RestrictedShell) execute(ctx context.Context, line string) (*filesystem.ExecuteResponse, error) {
	parsed, err := ParseCommand(line)
	if err != nil {
		return nil, err
	}
	if parsed.Kind == CommandEmbedText {
		return s.embedText(ctx, parsed.Args)
	}
	script, err := s.resolveScript(parsed.Script)
	if err != nil {
		return nil, err
	}
	args, err := mapScriptArgs(parsed.Args, s.resolveWorkspacePath)
	if err != nil {
		return nil, err
	}
	return s.runPython(ctx, script, args)
}

// resolveScript 在原始 Skill 目录里找脚本并返回原件的绝对路径；工作区里的副本永远不会被执行。
func (s *RestrictedShell) resolveScript(rel string) (string, error) {
	if err := checkRelPath(rel); err != nil {
		return "", err
	}
	candidate := filepath.Clean(filepath.Join(s.skillDir, filepath.FromSlash(rel)))
	if !withinRoot(s.skillDir, candidate) {
		return "", rejectf("脚本必须在 Skill 目录内：%q", rel)
	}
	info, err := os.Lstat(candidate)
	if errors.Is(err, fs.ErrNotExist) {
		return "", rejectf("Skill 目录里没有脚本 %q（只能运行 Skill 自带脚本，不能运行工作区里新写的脚本）", rel)
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", rejectf("脚本必须是普通文件：%q", rel)
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if !withinRoot(s.skillDir, real) {
		return "", rejectf("脚本经符号链接指向 Skill 目录之外：%q", rel)
	}
	return real, nil
}

// resolveWorkspacePath 校验工作区相对路径：静态规则 + 已存在的最深一级经 EvalSymlinks 后仍在工作区内。
// 返回相对工作区的路径（子进程 cwd 就是工作区）。
func (s *RestrictedShell) resolveWorkspacePath(rel string) (string, error) {
	if err := checkRelPath(rel); err != nil {
		return "", err
	}
	candidate := filepath.Clean(filepath.Join(s.workspace, filepath.FromSlash(rel)))
	if !withinRoot(s.workspace, candidate) {
		return "", rejectf("路径越出工作区：%q", rel)
	}
	existing := candidate
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", rejectf("路径越出工作区：%q", rel)
		}
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	if !withinRoot(s.workspace, real) {
		return "", rejectf("路径经符号链接指向工作区之外：%q", rel)
	}
	return filepath.Rel(s.workspace, candidate)
}

func (s *RestrictedShell) runPython(ctx context.Context, script string, args []string) (*filesystem.ExecuteResponse, error) {
	runCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	// -E 忽略 PYTHON* 环境变量；-s 不加载用户 site；-B 不写 .pyc（Skill 目录与 .app 都不该被写）；
	// -u 不缓冲；-X utf8 让中文输出在 Windows 上也是 UTF-8。
	argv := append([]string{"-E", "-s", "-B", "-u", "-X", "utf8", script}, args...)
	cmd := exec.CommandContext(runCtx, s.python, argv...)
	cmd.Dir = s.workspace
	cmd.Env = restrictedEnv(s.workspace, s.tmp, filepath.Dir(s.python))
	output := &cappedBuffer{max: maxShellOutputBytes}
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = 2 * time.Second
	configureChildProcess(cmd)

	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		code := exitTimeout
		return &filesystem.ExecuteResponse{
			Output:    output.String() + fmt.Sprintf("\n脚本运行超过 %s 被终止\n", s.timeout),
			ExitCode:  &code,
			Truncated: output.truncated,
		}, nil
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, fmt.Errorf("运行内置 Python: %w", err)
		}
		exitCode = exitErr.ExitCode()
	}
	return &filesystem.ExecuteResponse{Output: output.String(), ExitCode: &exitCode, Truncated: output.truncated}, nil
}

// restrictedEnv 从零构造子进程环境，绝不继承 os.Environ()（里面有上游 Key、代理等）。
func restrictedEnv(workspace, tmp, pythonBin string) []string {
	env := []string{
		"PATH=" + pythonBin,
		"HOME=" + workspace,
		"USERPROFILE=" + workspace,
		"SKILL_DIR=" + workspace,
		"WORK_DIR=" + workspace,
		"TMPDIR=" + tmp,
		"TEMP=" + tmp,
		"TMP=" + tmp,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
	}
	return append(env, platformEnv()...)
}

var _ filesystem.Shell = (*RestrictedShell)(nil)
