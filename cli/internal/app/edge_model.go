package app

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/qq788538-dotcom/tvmao-drama/cli/internal/client"
)

func newEdgeCommand(st *runtimeState) *cobra.Command {
	c := &cobra.Command{
		Use:   "edge",
		Short: "画布连线",
		Long: strings.TrimSpace(`
连线既是画布上的有向边，也是下游节点的数据依赖。

**连线数组的顺序就是模型输入的顺序**：按期望顺序依次建边即可，
不需要（也没有）额外的顺序字段。

建连线本身不触发生成——只有 miaoju node run 才会。
`),
	}
	c.PersistentFlags().Int64Var(&st.flagProject, "project", 0, "项目 ID（省略则用当前目录绑定）")
	c.AddCommand(newEdgeListCommand(st), newEdgeCreateCommand(st), newEdgeDeleteCommand(st))
	return c
}

func newEdgeListCommand(st *runtimeState) *cobra.Command {
	var to string
	c := &cobra.Command{
		Use: "list", Short: "列出连线（顺序即输入顺序）", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pid, cl, err := st.projectClient()
			if err != nil {
				return err
			}
			edges, err := cl.ListEdges(cmd.Context(), pid)
			if err != nil {
				return err
			}
			if to != "" {
				toID, err := cl.ResolveNode(cmd.Context(), pid, to)
				if err != nil {
					return err
				}
				filtered := make([]client.Edge, 0, len(edges))
				for _, e := range edges {
					if e.ToNodeID == toID {
						filtered = append(filtered, e)
					}
				}
				edges = filtered
			}
			return st.out.Emit(edges)
		},
	}
	c.Flags().StringVar(&to, "to", "", "只看指向该节点的入边（顺序即该节点的输入顺序）")
	return c
}

func newEdgeCreateCommand(st *runtimeState) *cobra.Command {
	var from, to string
	c := &cobra.Command{
		Use: "create", Short: "建立连线（同一对节点重复执行不会产生第二条）", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pid, cl, err := st.projectClient()
			if err != nil {
				return err
			}
			if from == "" || to == "" {
				return usagef("必须同时提供 --from 与 --to")
			}
			fromID, err := cl.ResolveNode(cmd.Context(), pid, from)
			if err != nil {
				return err
			}
			toID, err := cl.ResolveNode(cmd.Context(), pid, to)
			if err != nil {
				return err
			}
			e, err := cl.CreateEdge(cmd.Context(), pid, fromID, toID, st.idempotencyKey())
			if err != nil {
				return err
			}
			return st.out.Emit(e)
		},
	}
	c.Flags().StringVar(&from, "from", "", "上游节点引用")
	c.Flags().StringVar(&to, "to", "", "下游节点引用")
	return c
}

func newEdgeDeleteCommand(st *runtimeState) *cobra.Command {
	return &cobra.Command{
		Use: "delete <连线ID>", Short: "删除连线", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, cl, err := st.projectClient()
			if err != nil {
				return err
			}
			if err := cl.DeleteEdge(cmd.Context(), pid, args[0]); err != nil {
				return err
			}
			return st.out.Emit(map[string]any{"deleted": true, "edgeId": args[0]})
		},
	}
}

func newModelCommand(st *runtimeState) *cobra.Command {
	c := &cobra.Command{
		Use:   "model",
		Short: "查看可用模型与它们的输入 schema",
		Long: strings.TrimSpace(`
创建生成节点前先在这里确认模型能力：时长上限、比例、分辨率档位都在 inputSchema 里。

模型可以用稳定 ID 或精确展示名引用；展示名匹配到多个时会报错并列出候选，
不会替你挑一个。写入节点与任务的权威值始终是稳定 modelId。
`),
	}
	c.AddCommand(newModelListCommand(st), newModelGetCommand(st))
	return c
}

func newModelListCommand(st *runtimeState) *cobra.Command {
	var kind, search string
	c := &cobra.Command{
		Use: "list", Short: "列出可见模型", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := st.client().ListModels(cmd.Context(), kind, search)
			if err != nil {
				return err
			}
			return st.out.Emit(list)
		},
	}
	c.Flags().StringVar(&kind, "type", "", "按类型过滤：image / video")
	c.Flags().StringVar(&search, "search", "", "按 ID 或名称搜索（大小写不敏感）")
	return c
}

func newModelGetCommand(st *runtimeState) *cobra.Command {
	return &cobra.Command{
		Use: "get <模型ID或精确名称>", Short: "查看模型详情与公共输入 schema", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := st.client().GetModel(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return st.out.Emit(m)
		},
	}
}
