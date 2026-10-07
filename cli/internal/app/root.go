// Package app 组装 cobra 根命令。
//
// 两条结构性约束：
//
//  1. **无包级可变状态**。所有依赖经 Dependencies 传入，两个 root 实例可以
//     在同一进程里各写各的 stdout（测试就是这么跑的）。
//  2. **只有 main 调 os.Exit**。子命令一律 RunE 返回 typed error，
//     由最外层统一映射退出码。
package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/james/huabu_studio/apps/cli/internal/client"
	"github.com/james/huabu_studio/apps/cli/internal/endpoint"
	"github.com/james/huabu_studio/apps/cli/internal/exitcode"
	"github.com/james/huabu_studio/apps/cli/internal/output"
	"github.com/james/huabu_studio/apps/cli/internal/workspace"
)

type Dependencies struct {
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Version string
	// WorkDir 是解析 .miaoju/project.json 的基准目录。
	WorkDir string
	// EndpointDir 是 cli-endpoint.json 所在目录；空时用 endpoint.DefaultDir()。测试注入临时目录。
	EndpointDir string
}

// UsageError 表示参数/本地配置问题，映射退出码 2 并打印精简 usage。
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

func usagef(format string, a ...any) error {
	return &UsageError{Err: fmt.Errorf(format, a...)}
}

// runtimeState 是一次命令执行期间解析出来的上下文。
// 挂在 cobra command 的闭包里而不是包级变量——这是「无全局状态」的落点。
type runtimeState struct {
	deps Dependencies
	out  output.Writer

	flagOutput  string
	flagQuiet   bool
	flagReqID   string
	flagIdemKey string
	flagTrace   bool
	flagProject int64

	// cl 在一条命令内复用：连接（读地址文件 + ping）只做一次。
	cl *client.Client
}

// client 按需构造 HTTP client。地址文件懒加载——直到第一次发请求才读，
// 所以应用没开时 `miaoju version` 之类不联网的命令照样能跑。
func (s *runtimeState) client() *client.Client {
	if s.cl != nil {
		return s.cl
	}
	var trace func(string, ...any)
	if s.flagTrace {
		trace = func(format string, a ...any) { s.out.Warn("[trace] "+format, a...) }
	}
	s.cl = client.New(client.Options{
		Resolve:   s.resolveEndpoint,
		Version:   s.deps.Version,
		RequestID: s.flagReqID,
		Trace:     trace,
		Warn:      func(msg string) { s.out.Warn("%s", msg) },
	})
	return s.cl
}

// resolveEndpoint 每次调用都重新读地址文件（应用重启后端口与 token 会变）。
func (s *runtimeState) resolveEndpoint() (endpoint.Info, error) {
	dir := s.deps.EndpointDir
	if dir == "" {
		d, err := endpoint.DefaultDir()
		if err != nil {
			return endpoint.Info{}, err
		}
		dir = d
	}
	return endpoint.Load(dir)
}

// project 解析当前操作的项目。
func (s *runtimeState) project() (int64, error) {
	id, err := workspace.ResolveProject(s.flagProject, s.deps.WorkDir)
	if err != nil {
		return 0, &UsageError{Err: err}
	}
	return id, nil
}

// idempotencyKey 返回本次写请求的幂等键。
// 用户没固定时**自动生成**——默认就带幂等键，才让 client 的写重试是安全的。
func (s *runtimeState) idempotencyKey() string {
	if s.flagIdemKey != "" {
		return s.flagIdemKey
	}
	return newIdempotencyKey()
}

func NewRootCommand(deps Dependencies) *cobra.Command {
	if deps.Stdout == nil {
		deps.Stdout = os.Stdout
	}
	if deps.Stderr == nil {
		deps.Stderr = os.Stderr
	}
	if deps.Stdin == nil {
		deps.Stdin = os.Stdin
	}
	if deps.WorkDir == "" {
		if wd, err := os.Getwd(); err == nil {
			deps.WorkDir = wd
		}
	}
	st := &runtimeState{deps: deps}

	root := &cobra.Command{
		Use:   "miaoju",
		Short: "喵剧AI 自由画布命令行",
		Long: strings.TrimSpace(`
喵剧AI 自由画布命令行。

默认输出 JSON 到 stdout，进度与诊断走 stderr —— 可以直接 | jq 处理。

典型流程：
  miaoju project create --name "我的画布"
  miaoju project use <项目ID>
  miaoju asset upload ./ref.png --create-node
  miaoju node create --type image-generator --model <模型ID> --param prompt="雨后城市" --left <上游节点ID>
  miaoju node run <节点ID> --wait
  miaoju asset download --node <节点ID> --out ./out.png
`),
		SilenceUsage:  true,
		SilenceErrors: true,
		// 参数错误由这里统一包成 UsageError，映射退出码 2。
		// 业务错误走 RunE 返回，**不打印整页 usage**。
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return st.setup(cmd)
		},
	}
	root.SetOut(deps.Stdout)
	root.SetErr(deps.Stderr)
	root.SetIn(deps.Stdin)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &UsageError{Err: err}
	})

	// 根级 persistent flags 只放真正跨命令的。
	pf := root.PersistentFlags()
	pf.StringVarP(&st.flagOutput, "output", "o", "json", "输出格式：json / table / ndjson")
	pf.BoolVarP(&st.flagQuiet, "quiet", "q", false, "不输出进度（不影响警告与错误）")
	pf.StringVar(&st.flagReqID, "request-id", "", "自定义 X-Request-ID，便于排查")
	pf.StringVar(&st.flagIdemKey, "idempotency-key", "", "固定幂等键；默认每次命令自动生成")
	pf.BoolVar(&st.flagTrace, "trace", false, "打印请求方法与路径到 stderr（不含凭据与 body）")

	root.AddCommand(
		newVersionCommand(st),
		newProjectCommand(st),
		newCanvasCommand(st),
		newNodeCommand(st),
		newEdgeCommand(st),
		newModelCommand(st),
		newAssetCommand(st),
		newRunCommand(st),
		newComplianceCommand(st),
	)
	return root
}

// setup 在每条命令执行前解析输出格式。
func (s *runtimeState) setup(cmd *cobra.Command) error {
	format, err := output.ParseFormat(s.flagOutput)
	if err != nil {
		return &UsageError{Err: err}
	}
	s.out = output.New(format, s.deps.Stdout, s.deps.Stderr, s.flagQuiet)
	return nil
}

// Execute 跑根命令并返回退出码。main 是唯一调用它的地方。
func Execute(root *cobra.Command, args []string, stderr io.Writer) int {
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return exitcode.OK
	}
	return reportError(root, err, stderr)
}

func reportError(root *cobra.Command, err error, stderr io.Writer) int {
	var ue *UsageError
	if errors.As(err, &ue) {
		fmt.Fprintf(stderr, "错误：%v\n\n", ue.Err)
		// 参数错误才打 usage —— 业务错误打整页 usage 只会淹没真正的信息。
		fmt.Fprintf(stderr, "用法：%s\n运行 `miaoju --help` 查看完整帮助。\n", root.UseLine())
		return exitcode.Usage
	}

	// AppNotRunning/SandboxBlocked 必须先于 APIError 判断：
	// 401 连续两次后的 AppNotRunningError 里包着 APIError。
	var anr *client.AppNotRunningError
	if errors.As(err, &anr) {
		fmt.Fprintln(stderr, "喵剧未运行，请先打开应用")
		return exitcode.AppNotRunning
	}
	var sb *client.SandboxBlockedError
	if errors.As(err, &sb) {
		fmt.Fprintln(stderr, "网络被 AI 助手的沙箱拦截，请允许本命令访问网络后重试")
		return exitcode.SandboxBlocked
	}

	var ae *client.APIError
	if errors.As(err, &ae) {
		fmt.Fprintf(stderr, "错误：%s\n", ae.Message)
		if ae.Code != "" {
			fmt.Fprintf(stderr, "错误码：%s", ae.Code)
			if ae.RequestID != "" {
				fmt.Fprintf(stderr, "  request-id：%s", ae.RequestID)
			}
			fmt.Fprintln(stderr)
		}
		if cands, ok := ae.Details["candidates"]; ok {
			fmt.Fprintf(stderr, "候选：%v\n", cands)
		}
		return exitcode.FromAPIError(ae.Code, ae.Status)
	}

	var ne *client.NetworkError
	if errors.As(err, &ne) {
		fmt.Fprintf(stderr, "网络错误：%v\n", ne.Err)
		return exitcode.Network
	}

	fmt.Fprintf(stderr, "错误：%v\n", err)
	return exitcode.Server
}
