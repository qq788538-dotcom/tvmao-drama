package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/james/huabu_studio/apps/cli/internal/client"
)

// pollInterval 是 --wait 的轮询间隔。生成动辄几十秒，3 秒够密了。
const pollInterval = 3 * time.Second

func newNodeRunCommand(st *runtimeState) *cobra.Command {
	var (
		params   []string
		strParam []string
		model    string
		wait     bool
		timeout  time.Duration
	)
	c := &cobra.Command{
		Use:   "run <节点ID>",
		Short: "运行生成节点",
		Long: strings.TrimSpace(`
提交一次生成。这是 CLI 里唯一会消耗积分的命令。

--param 覆盖的参数会合并进节点并落盘，下次不带也生效。
节点若已在生成中，本命令会拒绝而不是再冻结一次积分。
节点若没有保存模型（历史节点常见），必须用 --model 显式指定——服务端不会替你猜。
`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, cl, err := st.projectClient()
			if err != nil {
				return err
			}
			nodeID, err := cl.ResolveNode(cmd.Context(), pid, args[0])
			if err != nil {
				return err
			}
			overrides, err := parseParams(params, strParam)
			if err != nil {
				return err
			}
			if model != "" {
				m, err := cl.GetModel(cmd.Context(), model)
				if err != nil {
					return err
				}
				overrides["modelId"] = m.ModelID
			}

			run, err := cl.RunNode(cmd.Context(), pid, nodeID, overrides, st.idempotencyKey())
			if err != nil {
				return err
			}
			if !wait {
				return st.out.Emit(run)
			}
			st.out.Progress("已提交任务 %d（模型 %s），等待完成…", run.TaskID, run.ModelID)
			status, err := pollRun(cmd.Context(), st, cl, run.TaskID, timeout)
			if err != nil {
				return err
			}
			return emitRunStatus(st, status)
		},
	}
	f := c.Flags()
	f.StringArrayVar(&params, "param", nil, "本次运行覆盖的参数 key=value，可重复；会写回节点")
	f.StringArrayVar(&strParam, "param-string", nil, "强制字符串的覆盖参数 key=value")
	f.StringVar(&model, "model", "", "本次运行使用的模型 ID 或精确展示名")
	f.BoolVar(&wait, "wait", false, "等待任务结束再返回")
	f.DurationVar(&timeout, "timeout", 30*time.Minute, "--wait 的最长等待时间")
	return c
}

func newRunCommand(st *runtimeState) *cobra.Command {
	c := &cobra.Command{Use: "run", Short: "查询与等待生成任务"}
	c.AddCommand(
		&cobra.Command{
			Use: "get <任务ID>", Short: "查询任务状态", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				id, err := parseTaskID(args[0])
				if err != nil {
					return err
				}
				status, err := st.client().GetRun(cmd.Context(), id)
				if err != nil {
					return err
				}
				return emitRunStatus(st, status)
			},
		},
		newRunWaitCommand(st),
	)
	return c
}

func newRunWaitCommand(st *runtimeState) *cobra.Command {
	var timeout time.Duration
	c := &cobra.Command{
		Use: "wait <任务ID>", Short: "轮询直到任务结束", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseTaskID(args[0])
			if err != nil {
				return err
			}
			status, err := pollRun(cmd.Context(), st, st.client(), id, timeout)
			if err != nil {
				return err
			}
			return emitRunStatus(st, status)
		},
	}
	c.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "最长等待时间")
	return c
}

// pollRun 轮询到终态。
//
// 只读 /runs/{taskID}，不在客户端另建一套状态机——任务真源永远在服务端。
func pollRun(ctx context.Context, st *runtimeState, cl *client.Client, taskID int64, timeout time.Duration) (client.RunStatus, error) {
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	waited := 0

	for {
		status, err := cl.GetRun(ctx, taskID)
		if err != nil {
			return client.RunStatus{}, err
		}
		if status.Status != "processing" {
			return status, nil
		}
		if time.Now().After(deadline) {
			return client.RunStatus{}, &client.NetworkError{
				Err: contextDeadline{taskID: taskID, waited: waited},
			}
		}
		waited += int(pollInterval / time.Second)
		st.out.Progress("生成中…已等待 %ds", waited)

		select {
		case <-ctx.Done():
			// Ctrl+C 只是停止等待，任务仍在服务端跑——说清楚，免得用户以为取消了。
			st.out.Warn("已停止等待；任务 %d 仍在服务端运行，可用 miaoju run get %d 查询", taskID, taskID)
			return client.RunStatus{}, &client.NetworkError{Err: ctx.Err()}
		case <-time.After(pollInterval):
		}
	}
}

type contextDeadline struct {
	taskID int64
	waited int
}

func (e contextDeadline) Error() string {
	return "等待超时（任务 " + strconv.FormatInt(e.taskID, 10) + " 仍在服务端运行，可用 miaoju run get 继续查询）"
}

// emitRunStatus 输出终态；失败任务映射到非 0 退出码。
func emitRunStatus(st *runtimeState, status client.RunStatus) error {
	// 无论成功失败都先把 JSON 写到 stdout —— 调用方需要拿到 error 字段，
	// 只把失败原因写 stderr 会让脚本读不到。
	if err := st.out.Emit(status); err != nil {
		return err
	}
	if status.Status == "failed" {
		return &client.APIError{
			Code: "TASK_FAILED", Status: 502,
			Message: firstNonEmptyStr(status.Error, "生成失败"),
		}
	}
	return nil
}

func parseTaskID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, usagef("任务 ID 必须是正整数，收到 %q", raw)
	}
	return id, nil
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
