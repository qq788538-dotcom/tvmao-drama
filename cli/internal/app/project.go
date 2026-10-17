package app

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/james/huabu_studio/apps/cli/internal/client"
	"github.com/james/huabu_studio/apps/cli/internal/workspace"
)

func newProjectCommand(st *runtimeState) *cobra.Command {
	c := &cobra.Command{Use: "project", Short: "自由画布项目的增删改查与本地绑定"}
	c.AddCommand(
		newProjectListCommand(st),
		newProjectCreateCommand(st),
		newProjectGetCommand(st),
		newProjectUpdateCommand(st),
		newProjectDeleteCommand(st),
		newProjectUseCommand(st),
		newProjectUnuseCommand(st),
	)
	return c
}

func newProjectListCommand(st *runtimeState) *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "列出我的项目", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := st.client().ListProjects(cmd.Context())
			if err != nil {
				return err
			}
			if list == nil {
				list = nil
			}
			return st.out.Emit(list)
		},
	}
}

func newProjectCreateCommand(st *runtimeState) *cobra.Command {
	var name, desc string
	var use bool
	c := &cobra.Command{
		Use: "create", Short: "创建项目", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(name) == "" {
				return usagef("必须提供 --name")
			}
			p, err := st.client().CreateProject(cmd.Context(), name, desc, st.idempotencyKey())
			if err != nil {
				return err
			}
			if use {
				if err := workspace.SaveProjectBinding(st.deps.WorkDir,
					workspace.ProjectBinding{ProjectID: p.ID}); err != nil {
					return err
				}
				st.out.Progress("已绑定到当前目录：%s", workspace.BindingPath(st.deps.WorkDir))
			}
			return st.out.Emit(p)
		},
	}
	c.Flags().StringVar(&name, "name", "", "项目名称（必填）")
	c.Flags().StringVar(&desc, "description", "", "项目简介")
	c.Flags().BoolVar(&use, "use", false, "创建后立即绑定到当前目录")
	return c
}

func newProjectGetCommand(st *runtimeState) *cobra.Command {
	return &cobra.Command{
		Use: "get [项目ID]", Short: "查看项目详情（省略 ID 时用当前目录绑定）", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := projectFromArgOrBinding(st, args)
			if err != nil {
				return err
			}
			p, err := st.client().GetProject(cmd.Context(), id)
			if err != nil {
				return err
			}
			return st.out.Emit(p)
		},
	}
}

func newProjectUpdateCommand(st *runtimeState) *cobra.Command {
	var name, desc string
	c := &cobra.Command{
		Use: "update [项目ID]", Short: "修改项目名称或简介", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := projectFromArgOrBinding(st, args)
			if err != nil {
				return err
			}
			// 用 flag 是否被显式设置来区分「不改」与「改成空」——
			// 只看值是否为空的话，永远没法把简介清空。
			var namePtr, descPtr *string
			if cmd.Flags().Changed("name") {
				namePtr = &name
			}
			if cmd.Flags().Changed("description") {
				descPtr = &desc
			}
			if namePtr == nil && descPtr == nil {
				return usagef("至少要改一个字段：--name 或 --description")
			}
			p, err := st.client().UpdateProject(cmd.Context(), id, namePtr, descPtr)
			if err != nil {
				return err
			}
			return st.out.Emit(p)
		},
	}
	c.Flags().StringVar(&name, "name", "", "新名称")
	c.Flags().StringVar(&desc, "description", "", "新简介")
	return c
}

func newProjectDeleteCommand(st *runtimeState) *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use: "delete <项目ID>", Short: "删除项目", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || id <= 0 {
				return usagef("项目 ID 必须是正整数，收到 %q", args[0])
			}
			if err := confirmDestructive(cmd.Context(), st, yes, "删除项目 %d", id); err != nil {
				return err
			}
			if err := st.client().DeleteProject(cmd.Context(), id); err != nil {
				return err
			}
			return st.out.Emit(map[string]any{"deleted": true, "projectId": id})
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "跳过确认（非交互环境必须显式传）")
	return c
}

func newProjectUseCommand(st *runtimeState) *cobra.Command {
	return &cobra.Command{
		Use: "use <项目ID>", Short: "把当前目录绑定到某个项目", Args: cobra.ExactArgs(1),
		Long: strings.TrimSpace(`
在当前目录写 .miaoju/project.json，之后本目录下的命令不必再传 --project。

绑定**不向父目录查找**——cd 进子目录不会继承父目录的绑定。
`),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || id <= 0 {
				return usagef("项目 ID 必须是正整数，收到 %q", args[0])
			}
			// 先确认项目真的存在且属于自己，避免绑一个打不开的 ID。
			if _, err := st.client().GetProject(cmd.Context(), id); err != nil {
				return err
			}
			if err := workspace.SaveProjectBinding(st.deps.WorkDir,
				workspace.ProjectBinding{ProjectID: id}); err != nil {
				return err
			}
			return st.out.Emit(map[string]any{
				"projectId":   id,
				"bindingPath": workspace.BindingPath(st.deps.WorkDir),
			})
		},
	}
}

func newProjectUnuseCommand(st *runtimeState) *cobra.Command {
	return &cobra.Command{
		Use: "unuse", Short: "解除当前目录的项目绑定", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := workspace.RemoveProjectBinding(st.deps.WorkDir); err != nil {
				return err
			}
			return st.out.Emit(map[string]any{"unbound": true})
		},
	}
}

func projectFromArgOrBinding(st *runtimeState, args []string) (int64, error) {
	if len(args) == 1 {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || id <= 0 {
			return 0, usagef("项目 ID 必须是正整数，收到 %q", args[0])
		}
		return id, nil
	}
	return st.project()
}

// confirmDestructive 处理破坏性操作的确认。
// **非 TTY 且未传 --yes 时直接失败，不等待交互**——脚本里挂起比报错更糟。
func confirmDestructive(ctx context.Context, st *runtimeState, yes bool, format string, a ...any) error {
	if yes {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return usagef("非交互环境执行破坏性操作必须显式传 --yes")
	}
	st.out.Warn("即将"+format+"，输入 y 确认：", a...)
	var answer string
	if _, err := readLine(ctx, st, &answer); err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		return usagef("已取消")
	}
	return nil
}

// readLine 读一行确认输入。读 stdin 不响应 context，放到 goroutine 里：Ctrl+C（context 取消）时立即按
// 「用户取消」返回，不用再按一次（goroutine 随进程退出）。
func readLine(ctx context.Context, st *runtimeState, out *string) (int, error) {
	type result struct {
		s   string
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := st.deps.Stdin.Read(buf)
		ch <- result{string(buf[:n]), n, err}
	}()
	select {
	case <-ctx.Done():
		return 0, &client.NetworkError{Err: ctx.Err()}
	case r := <-ch:
		if r.n > 0 {
			*out = r.s
		}
		if r.err != nil && r.n == 0 {
			return 0, r.err
		}
		return r.n, nil
	}
}
