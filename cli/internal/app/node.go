package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/james/huabu_studio/apps/cli/internal/client"
)

func newCanvasCommand(st *runtimeState) *cobra.Command {
	c := &cobra.Command{Use: "canvas", Short: "读取整张画布"}
	c.AddCommand(&cobra.Command{
		Use: "get", Short: "输出规范化的节点、连线与快照版本", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pid, err := st.project()
			if err != nil {
				return err
			}
			canvas, err := st.client().GetCanvas(cmd.Context(), pid)
			if err != nil {
				return err
			}
			return st.out.Emit(canvas)
		},
	})
	c.PersistentFlags().Int64Var(&st.flagProject, "project", 0, "项目 ID（省略则用当前目录绑定）")
	return c
}

func newNodeCommand(st *runtimeState) *cobra.Command {
	c := &cobra.Command{Use: "node", Short: "画布节点的增删改查与运行"}
	c.PersistentFlags().Int64Var(&st.flagProject, "project", 0, "项目 ID（省略则用当前目录绑定）")
	c.AddCommand(
		newNodeListCommand(st),
		newNodeCreateCommand(st),
		newNodeGetCommand(st),
		newNodeUpdateCommand(st),
		newNodeMoveCommand(st),
		newNodeDeleteCommand(st),
		newNodeRunCommand(st),
	)
	return c
}

func newNodeListCommand(st *runtimeState) *cobra.Command {
	var nodeType, status string
	c := &cobra.Command{
		Use: "list", Short: "列出节点", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pid, cl, err := st.projectClient()
			if err != nil {
				return err
			}
			nodes, err := cl.ListNodes(cmd.Context(), pid, nodeType, status)
			if err != nil {
				return err
			}
			return st.out.Emit(nodes)
		},
	}
	c.Flags().StringVar(&nodeType, "type", "", "按语义类型过滤，如 image-generator")
	c.Flags().StringVar(&status, "status", "", "按状态过滤：idle/generating/succeeded/failed")
	return c
}

func newNodeGetCommand(st *runtimeState) *cobra.Command {
	return &cobra.Command{
		Use: "get <节点ID>", Short: "查看节点详情（含派生输入摘要）", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, cl, err := st.projectClient()
			if err != nil {
				return err
			}
			id, err := cl.ResolveNode(cmd.Context(), pid, args[0])
			if err != nil {
				return err
			}
			n, err := cl.GetNode(cmd.Context(), pid, id)
			if err != nil {
				return err
			}
			return st.out.Emit(n)
		},
	}
}

func newNodeCreateCommand(st *runtimeState) *cobra.Command {
	var (
		nodeType string
		model    string
		content  string
		params   []string
		strParam []string
		lefts    []string
		x, y     float64
		width    float64
		height   float64
	)
	c := &cobra.Command{
		Use:   "create",
		Short: "创建节点，并可同时建立上游连线",
		Long: strings.TrimSpace(`
创建一个语义节点。可写类型只有四种：
  image-input / audio-input / video-input / image-generator / video-generator

生成节点**必须**用 --model 显式指定模型：服务端不会替你挑一个。
（生产里 52% 的历史生成节点没保存模型，那些结果已经无法追溯用了哪个模型。）

--left 可重复传入，按传入顺序建边。**连线数组的顺序就是模型输入的顺序**，
所以「第一张参考图」= 第一个 --left。

创建与运行严格分离：本命令只建节点和连线，不冻结积分、不提交生成。
`),
		Example: strings.TrimSpace(`
  miaoju node create --type image-input --content https://...
  miaoju node create --type image-generator --model doubao-image \
    --param prompt="雨后城市街道，电影感" --param ratio=16:9 \
    --left n-abc12345 --left n-def67890`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pid, cl, err := st.projectClient()
			if err != nil {
				return err
			}
			if nodeType == "" {
				return usagef("必须提供 --type（可选 image-input / audio-input / video-input / image-generator / video-generator）")
			}
			parsed, err := parseParams(params, strParam)
			if err != nil {
				return err
			}
			if model != "" {
				// 模型名可能是展示名；解析成稳定 modelId 后再写入节点，
				// 避免展示名改了以后节点指向一个不存在的模型。
				m, err := cl.GetModel(cmd.Context(), model)
				if err != nil {
					return err
				}
				parsed["modelId"] = m.ModelID
			}
			// 生成节点缺模型在本地就拦下，省一次往返。
			if (nodeType == "image-generator" || nodeType == "video-generator") && parsed["modelId"] == nil {
				return usagef("生成节点必须用 --model 指定模型；用 miaoju model list 查看可用模型")
			}

			in := client.CreateNodeInput{Type: nodeType, Content: content, Params: parsed}
			if cmd.Flags().Changed("x") || cmd.Flags().Changed("y") {
				in.Position = &client.Position{X: x, Y: y}
			}
			if cmd.Flags().Changed("width") || cmd.Flags().Changed("height") {
				in.Size = &client.Size{Width: width, Height: height}
			}

			node, err := cl.CreateNode(cmd.Context(), pid, in, st.idempotencyKey(), 0)
			if err != nil {
				return err
			}

			// 建边是第二步，失败不回滚已建的节点——但必须如实报告建到了哪一步，
			// 部分成功远好过静默丢失。
			edges := make([]client.Edge, 0, len(lefts))
			for i, ref := range lefts {
				fromID, err := cl.ResolveNode(cmd.Context(), pid, ref)
				if err != nil {
					st.out.Warn("节点 %s 已创建，但第 %d 个 --left（%s）解析失败", node.ID, i+1, ref)
					return err
				}
				e, err := cl.CreateEdge(cmd.Context(), pid, fromID, node.ID, st.idempotencyKey())
				if err != nil {
					st.out.Warn("节点 %s 已创建，已建 %d 条边，第 %d 条失败", node.ID, len(edges), i+1)
					return err
				}
				edges = append(edges, e)
			}
			return st.out.Emit(map[string]any{"node": node, "edges": edges})
		},
	}
	f := c.Flags()
	f.StringVar(&nodeType, "type", "", "节点类型（必填）")
	f.StringVar(&model, "model", "", "模型 ID 或精确展示名；生成节点必填")
	f.StringVar(&content, "content", "", "输入节点的媒体 URL")
	// StringArrayVar 而不是 StringSliceVar：后者会按逗号拆值，
	// 提示词里出现逗号（中文场景几乎必然）就会被劈成两半。
	f.StringArrayVar(&params, "param", nil, "生成参数 key=value，可重复；值按字面推断类型")
	f.StringArrayVar(&strParam, "param-string", nil, "强制按字符串处理的参数 key=value，可重复")
	f.StringArrayVar(&lefts, "left", nil, "上游节点引用，可重复；**传入顺序即输入顺序**")
	f.Float64Var(&x, "x", 0, "节点左上角 X")
	f.Float64Var(&y, "y", 0, "节点左上角 Y")
	f.Float64Var(&width, "width", 0, "节点宽度（省略用与网页一致的默认值）")
	f.Float64Var(&height, "height", 0, "节点高度")
	return c
}

func newNodeUpdateCommand(st *runtimeState) *cobra.Command {
	var (
		params   []string
		strParam []string
		content  string
		lefts    []string
		leftRms  []string
		x, y     float64
		version  int64
	)
	c := &cobra.Command{
		Use:   "update <节点ID>",
		Short: "局部修改节点（merge patch，不会覆盖未传的字段）",
		Long: strings.TrimSpace(`
局部修改节点参数、内容或位置，并可增删上游连线。

--left 是「确保这条边存在」：重复执行同一条命令不会攒出重复边。
--left-rm 是「确保这条边不存在」：目标边本来就没有时视为成功。
两者都可重复传入，先执行全部 --left-rm 再执行全部 --left，
这样「换掉某个上游」可以在一条命令里完成且顺序可预期。
`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, cl, err := st.projectClient()
			if err != nil {
				return err
			}
			id, err := cl.ResolveNode(cmd.Context(), pid, args[0])
			if err != nil {
				return err
			}
			parsed, err := parseParams(params, strParam)
			if err != nil {
				return err
			}
			in := client.PatchNodeInput{}
			if len(parsed) > 0 {
				in.Params = parsed
			}
			if cmd.Flags().Changed("content") {
				in.Content = &content
			}
			if cmd.Flags().Changed("x") || cmd.Flags().Changed("y") {
				in.Position = &client.Position{X: x, Y: y}
			}
			hasEdgeOps := len(lefts) > 0 || len(leftRms) > 0
			if in.Params == nil && in.Content == nil && in.Position == nil && !hasEdgeOps {
				return usagef("没有要修改的内容：用 --param / --content / --x --y / --left / --left-rm")
			}

			// 先删后加：这样「把上游 A 换成 B」在一条命令里的效果是确定的。
			removed, err := removeUpstreamEdges(cmd.Context(), st, cl, pid, id, leftRms)
			if err != nil {
				return err
			}
			added, err := addUpstreamEdges(cmd.Context(), st, cl, pid, id, lefts)
			if err != nil {
				return err
			}

			var node any
			if in.Params != nil || in.Content != nil || in.Position != nil {
				n, err := cl.PatchNode(cmd.Context(), pid, id, in, version)
				if err != nil {
					return err
				}
				node = n
			} else {
				n, err := cl.GetNode(cmd.Context(), pid, id)
				if err != nil {
					return err
				}
				node = n
			}
			if !hasEdgeOps {
				return st.out.Emit(node)
			}
			return st.out.Emit(map[string]any{
				"node": node, "edgesAdded": added, "edgesRemoved": removed,
			})
		},
	}
	f := c.Flags()
	f.StringArrayVar(&params, "param", nil, "要修改的参数 key=value，可重复")
	f.StringArrayVar(&strParam, "param-string", nil, "强制字符串的参数 key=value，可重复")
	f.StringVar(&content, "content", "", "节点内容 URL")
	f.StringArrayVar(&lefts, "left", nil, "确保存在来自该上游的连线，可重复；追加在现有入边之后")
	f.StringArrayVar(&leftRms, "left-rm", nil, "移除来自该上游的连线，可重复；本来就没有时视为成功")
	f.Float64Var(&x, "x", 0, "节点左上角 X")
	f.Float64Var(&y, "y", 0, "节点左上角 Y")
	f.Int64Var(&version, "snapshot-version", 0, "乐观并发保护：画布版本不符则拒绝写入")
	return c
}

func newNodeMoveCommand(st *runtimeState) *cobra.Command {
	var (
		sets    []string
		file    string
		version int64
	)
	c := &cobra.Command{
		Use:   "move",
		Short: "一次请求批量修改多个节点的位置 / 尺寸",
		Long: strings.TrimSpace(`
批量摆位。多个节点一条命令、一次请求、画布版本只 +1，整批原子（任一节点 ID
不存在则全部不落地）。

为什么要有它：布局一集常要挪十几个节点，逐个 node update 会让 agent 每挪一个
就停下来思考一轮，还要反复 canvas get 追版本号——十几个节点花几分钟，其中网络
只占十几秒。

--set 只接受节点 ID（不做引用解析，省一次往返）；只改传了的字段，其余不动。
不看节点可写类型：坐标不属于内容/参数，网页上任何节点都能拖。

--snapshot-version 是可选的：只有「先读画布、据此决定布局」的写入才需要它。
盲写（坐标是你自己算的，与别人改了什么无关）不必带。
`),
		Example: strings.TrimSpace(`
  miaoju node move --set n-aaa=0,500 --set n-bbb=400,500 --set n-ccc=800,500,320,240
  miaoju node move --file layout.json        # [{"id":"n-aaa","x":0,"y":500}, ...]
  cat layout.json | miaoju node move --file -`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pid, cl, err := st.projectClient()
			if err != nil {
				return err
			}
			patches, err := parseGeometrySets(sets)
			if err != nil {
				return err
			}
			if file != "" {
				fromFile, err := readGeometryFile(file, st.deps.Stdin)
				if err != nil {
					return err
				}
				patches = append(patches, fromFile...)
			}
			if len(patches) == 0 {
				return usagef("没有要移动的节点：用 --set <节点ID>=<x>,<y>[,<width>,<height>] 或 --file")
			}
			res, err := cl.PatchNodePositions(cmd.Context(), pid, patches, version)
			if err != nil {
				return err
			}
			return st.out.Emit(res)
		},
	}
	f := c.Flags()
	f.StringArrayVar(&sets, "set", nil, "<节点ID>=<x>,<y>[,<width>,<height>]，可重复；留空的分量不动，如 n-a=,500 只改 y")
	f.StringVar(&file, "file", "", "JSON 数组文件 [{\"id\",\"x\",\"y\",\"width\",\"height\"}]，- 读 stdin")
	f.Int64Var(&version, "snapshot-version", 0, "乐观并发保护：画布版本不符则整批拒绝")
	return c
}

// parseGeometrySets 解析 --set <id>=<x>,<y>[,<w>,<h>]。
// 分量允许留空表示不动（n-a=,500 只改 y），否则「只想改 y」得先查 x 再原样写回，
// 又多一次往返。
func parseGeometrySets(sets []string) ([]client.GeometryPatch, error) {
	out := make([]client.GeometryPatch, 0, len(sets))
	for _, raw := range sets {
		id, spec, err := splitKV(raw)
		if err != nil {
			return nil, err
		}
		parts := strings.Split(spec, ",")
		if len(parts) < 2 || len(parts) > 4 {
			return nil, usagef("--set 格式应为 <节点ID>=<x>,<y>[,<width>,<height>]，收到 %q", raw)
		}
		p := client.GeometryPatch{ID: strings.TrimSpace(id)}
		targets := []**float64{&p.X, &p.Y, &p.Width, &p.Height}
		names := []string{"x", "y", "width", "height"}
		for i, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			v, err := strconv.ParseFloat(part, 64)
			if err != nil {
				return nil, usagef("--set %s 的 %s 不是数字：%q", id, names[i], part)
			}
			*targets[i] = &v
		}
		if p.X == nil && p.Y == nil && p.Width == nil && p.Height == nil {
			return nil, usagef("--set %s 没有任何要改的分量", id)
		}
		out = append(out, p)
	}
	return out, nil
}

func readGeometryFile(path string, stdin io.Reader) ([]client.GeometryPatch, error) {
	var blob []byte
	var err error
	if path == "-" {
		blob, err = io.ReadAll(stdin)
	} else {
		blob, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, usagef("读取 --file 失败：%v", err)
	}
	var out []client.GeometryPatch
	if err := json.Unmarshal(blob, &out); err != nil {
		return nil, usagef("--file 应是 JSON 数组 [{\"id\":\"n-…\",\"x\":0,\"y\":0}]：%v", err)
	}
	return out, nil
}

func newNodeDeleteCommand(st *runtimeState) *cobra.Command {
	var yes bool
	var version int64
	c := &cobra.Command{
		Use: "delete <节点ID>", Short: "删除节点及其全部连线", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, cl, err := st.projectClient()
			if err != nil {
				return err
			}
			id, err := cl.ResolveNode(cmd.Context(), pid, args[0])
			if err != nil {
				return err
			}
			if err := confirmDestructive(st, yes, "删除节点 %s 及其全部连线", id); err != nil {
				return err
			}
			if err := cl.DeleteNode(cmd.Context(), pid, id, version); err != nil {
				return err
			}
			return st.out.Emit(map[string]any{"deleted": true, "nodeId": id})
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "跳过确认（非交互环境必须显式传）")
	c.Flags().Int64Var(&version, "snapshot-version", 0, "乐观并发保护")
	return c
}

// parseParams 把 --param key=value 解析成 JSON 值。
//
// 类型推断规则：能解析成整数/浮点/布尔的按对应类型，其余按字符串。
// prompt 例外——恒为字符串，否则 `--param prompt=2026` 会变成数字。
// 需要强制字符串时用 --param-string。
func parseParams(params, strParams []string) (map[string]any, error) {
	out := map[string]any{}
	for _, kv := range params {
		k, v, err := splitKV(kv)
		if err != nil {
			return nil, err
		}
		out[k] = inferParamValue(k, v)
	}
	for _, kv := range strParams {
		k, v, err := splitKV(kv)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

func splitKV(kv string) (string, string, error) {
	// 只按第一个 = 拆：值里出现 = 是常态（URL、base64）。
	i := strings.Index(kv, "=")
	if i <= 0 {
		return "", "", usagef("参数格式应为 key=value，收到 %q", kv)
	}
	return kv[:i], kv[i+1:], nil
}

// alwaysStringParams 是即使看起来像数字也必须当字符串的键。
var alwaysStringParams = map[string]bool{"prompt": true, "modelId": true, "ratio": true, "size": true}

func inferParamValue(key, raw string) any {
	if alwaysStringParams[key] {
		return raw
	}
	if raw == "true" {
		return true
	}
	if raw == "false" {
		return false
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}
	return raw
}

// projectClient 是「需要项目」这个前置的快捷方式，顺带造好 client。
func (s *runtimeState) projectClient() (int64, *client.Client, error) {
	pid, err := s.project()
	if err != nil {
		return 0, nil, err
	}
	return pid, s.client(), nil
}

// addUpstreamEdges 按传入顺序确保每条上游连线存在。
// 服务端对同 (from,to) 幂等，所以重复执行不会攒出重复边。
func addUpstreamEdges(ctx context.Context, st *runtimeState, cl *client.Client, pid int64, toID string, refs []string) ([]client.Edge, error) {
	out := make([]client.Edge, 0, len(refs))
	for i, ref := range refs {
		fromID, err := cl.ResolveNode(ctx, pid, ref)
		if err != nil {
			return out, fmt.Errorf("第 %d 个 --left（%s）解析失败: %w", i+1, ref, err)
		}
		e, err := cl.CreateEdge(ctx, pid, fromID, toID, st.idempotencyKey())
		if err != nil {
			return out, err
		}
		out = append(out, e)
	}
	return out, nil
}

// removeUpstreamEdges 移除来自指定上游的连线。
// 目标边不存在时视为成功——命令语义是「确保它不在」，不是「删掉它」。
func removeUpstreamEdges(ctx context.Context, st *runtimeState, cl *client.Client, pid int64, toID string, refs []string) ([]string, error) {
	if len(refs) == 0 {
		return []string{}, nil
	}
	edges, err := cl.ListEdges(ctx, pid)
	if err != nil {
		return nil, err
	}
	removed := []string{}
	for i, ref := range refs {
		fromID, err := cl.ResolveNode(ctx, pid, ref)
		if err != nil {
			return removed, fmt.Errorf("第 %d 个 --left-rm（%s）解析失败: %w", i+1, ref, err)
		}
		for _, e := range edges {
			if e.FromNodeID != fromID || e.ToNodeID != toID {
				continue
			}
			if err := cl.DeleteEdge(ctx, pid, e.ID); err != nil {
				return removed, err
			}
			removed = append(removed, e.ID)
		}
	}
	return removed, nil
}
