package app

import (
	"strings"

	"github.com/spf13/cobra"
)

// Seedance 2.0 合规校验。
//
// 为什么 CLI 必须有这个：doubao-seedance* 的生成强制要求输入素材先过合规校验，
// 没过就直接失败。以前提交校验只有网页一条路，于是用 CLI 能把整张画布建好，
// 一跑 seedance 就卡住，还得回网页对每张图手点一次——素材一多（6 张人物三视图
// + 5 张场景图）这就不是麻烦而是走不通。

func newComplianceCommand(st *runtimeState) *cobra.Command {
	c := &cobra.Command{
		Use:   "compliance",
		Short: "Seedance 2.0 合规校验",
		Long: strings.TrimSpace(`
把画布素材提交到 Seedance 做合规校验，并查看校验状态。

doubao-seedance* 的生成要求输入素材**先通过合规校验**，否则任务会失败。
典型流程：先把图片节点都生成出来 → verify 逐个提交 → status 等它们变成
active → 再跑视频生成。

校验是异步的：verify 提交后状态多半还是 pending，过一会儿用 status 再看。
重复提交是安全的——已通过且没换过素材的不会再占用上游配额。
`),
	}
	c.AddCommand(newComplianceVerifyCommand(st), newComplianceStatusCommand(st))
	c.PersistentFlags().Int64Var(&st.flagProject, "project", 0, "项目 ID（省略则用当前目录绑定）")
	return c
}

func newComplianceVerifyCommand(st *runtimeState) *cobra.Command {
	return &cobra.Command{
		Use:   "verify <节点ID>...",
		Short: "提交节点素材做合规校验",
		Long: strings.TrimSpace(`
提交一个或多个节点的素材去做合规校验。

只有带素材的节点能提交：图片输入/图片生成按图片校验，视频生成按视频校验，
音频输入按音频校验。还没生成出内容的节点会被拒绝——那时候没东西可校验。

传多个节点 ID 时逐个提交，中途失败会**停在那里**并返回非零退出码，
已提交的不受影响（服务端幂等，重跑整条命令是安全的）。
`),
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, cl, err := st.projectClient()
			if err != nil {
				return err
			}
			out := make([]any, 0, len(args))
			for _, ref := range args {
				nodeID, err := cl.ResolveNode(cmd.Context(), pid, ref)
				if err != nil {
					return err
				}
				st.out.Progress("提交合规校验：%s", nodeID)
				rec, err := cl.VerifyCompliance(cmd.Context(), pid, nodeID)
				if err != nil {
					return err
				}
				out = append(out, rec)
			}
			// 单个节点时直接给对象，多个才给数组——脚本里 `miaoju compliance
			// verify n-x | jq .status` 是最常见的用法，不该被数组包一层。
			if len(out) == 1 {
				return st.out.Emit(out[0])
			}
			return st.out.Emit(out)
		},
	}
}

func newComplianceStatusCommand(st *runtimeState) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "查看项目内所有素材的合规校验状态",
		Long: strings.TrimSpace(`
列出项目里每个带素材节点的校验状态，按画布里的节点顺序输出。

status 取值：
  unverified        还没提交过校验
  pending           已提交，上游还在处理
  active            通过，可以用于 seedance 生成
  failed            这次失败（可重试）
  failed_permanent  判定不合规，重试也没用，换素材吧
`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pid, cl, err := st.projectClient()
			if err != nil {
				return err
			}
			list, err := cl.ListCompliance(cmd.Context(), pid)
			if err != nil {
				return err
			}
			return st.out.Emit(list)
		},
	}
}
