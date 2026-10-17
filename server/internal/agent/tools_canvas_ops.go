// Package agent — tools_canvas_ops.go：让 planner 直接操作画布的一组内置工具
// （agent 拥有画布完整操作权）。
//
// 架构：读走服务端（直读 workflow_snapshots 最新快照，浏览器局部 mutation 秒级
// 同步上来，滞后可忽略）；写走前端（emit artifact → onAgentArtifact 调 store
// 动作执行）。写必须经前端的原因：画布是「浏览器持编辑锁 + 局部 mutation 上推」
// 模型，服务端绕过浏览器直写 DB，浏览器内存里的画布不知情，下一个用户操作就会
// 互相覆盖。前端执行天然获得 UI 实时可见 + undo 可回退 + 既有持久化路。
//
// 两条数据完整性红线（与权限无关）：
//  1. 机器管理字段（status / agentMeta / genHistory）不可写——直写会伪造
//     「生成中」的假 UI 状态、破坏产物溯源；
//  2. 画布操作本身绝不隐式触发生成/扣费——生成走 planner 已有的计费工具。
//
// V1 是盲发：工具 emit 后立即返回，前端执行成败 agent 不知道。所有写操作的
// 返回文案统一为「已提交…」而非「已完成」，避免 planner 过度承诺。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/james/huabu_studio/apps/api/internal/agent/canvasmanifest"
)

// CanvasSnapshotReader 是 read_canvas 需要的最小能力（projects.MySQLRepository
// 直接满足）。窄接口方便单测 stub。
type CanvasSnapshotReader interface {
	GetLatestSnapshot(projectID int64) (json.RawMessage, error)
}

const (
	ReadCanvasToolName       = "read_canvas"
	CreateNodeToolName       = "create_node"
	UpdateNodeToolName       = "update_node"
	DeleteNodeToolName       = "delete_node"
	ConnectNodesToolName     = "connect_nodes"
	RemoveConnectionToolName = "remove_connection"
)

// machineManagedSettings 是 agent 不可写的 settings 字段（红线 1）。
var machineManagedSettings = map[string]bool{
	"status":     true,
	"agentMeta":  true,
	"genHistory": true,
}

// knownNodeTypes 镜像前端 registry 的节点类型清单。未知类型直接拒绝——错拼的
// 类型落到画布上是一个渲染不出来的死节点。清单变更时两侧同步（低频）。
var knownNodeTypes = []string{
	"input-image", "input-audio", "video-input",
	"gen-image", "gen-video", "gen-audio",
	"text-node", "preview",
}

func isKnownNodeType(t string) bool {
	for _, k := range knownNodeTypes {
		if k == t {
			return true
		}
	}
	return false
}

const opSubmittedSuffix = "（指令已提交，画布稍后生效；如需确认结果可调用 read_canvas 复查。）"

// ---- read_canvas ----

type ReadCanvasTool struct {
	Reader CanvasSnapshotReader
}

func (t *ReadCanvasTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: ReadCanvasToolName,
		Desc: "Read the current canvas: all nodes (id, type, name, prompt/text preview, position, status, plus scalar settings such as modelId/ratio) and connections. Call this BEFORE modifying the canvas so you operate on real node ids and current values. For the FULL settings of one node and what you are allowed to write to it, call " + DescribeCanvasNodeToolName + ".",
		// 无参数工具也要给个空 schema，部分上游对缺 parameters 的工具报错。
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}, nil
}

// readCanvasNode 是快照节点的精简视图。字段极简是刻意的：200 节点画布控制在
// 几千 token 内，提示词/正文给截断预览，全文让 planner 用 @ 或 update 前再读。
//
// Settings 是按能力清单过滤出来的**标量**字段（modelId / ratio / 各类开关）。
// 只给标量是这一层的 token 纪律：数组和对象（script.rows、video-input.frames）
// 展开会把列表视图撑爆，它们由 describe_canvas_node 单节点返回全文。但标量必须
// 给——「哪些节点用的什么模型」这种判断要在列表层就能做出来，否则 agent 得对
// 每个节点各调一次详情才知道该改谁。
type readCanvasNode struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Name     string         `json:"name,omitempty"`
	Prompt   string         `json:"prompt,omitempty"`
	Text     string         `json:"text,omitempty"`
	HasMedia bool           `json:"has_media,omitempty"`
	Status   string         `json:"status,omitempty"`
	Settings map[string]any `json:"settings,omitempty"`
	X        float64        `json:"x"`
	Y        float64        `json:"y"`
	W        float64        `json:"width,omitempty"`
	H        float64        `json:"height,omitempty"`
}

type readCanvasConn struct {
	From      string `json:"from"`
	To        string `json:"to"`
	InputType string `json:"input_type,omitempty"`
}

func (t *ReadCanvasTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	projectID := ProjectIDFromContext(ctx)
	if t.Reader == nil || projectID == 0 {
		return "read_canvas 不可用：画布快照未接入。", nil
	}
	payload, err := t.Reader.GetLatestSnapshot(projectID)
	if err != nil {
		return fmt.Sprintf("读取画布失败：%v", err), nil
	}
	var doc struct {
		Nodes []struct {
			ID       string          `json:"id"`
			Type     string          `json:"type"`
			Content  string          `json:"content"`
			Status   string          `json:"status"`
			X        float64         `json:"x"`
			Y        float64         `json:"y"`
			Width    float64         `json:"width"`
			Height   float64         `json:"height"`
			Settings json.RawMessage `json:"settings"`
		} `json:"nodes"`
		Connections []struct {
			From      string `json:"from"`
			To        string `json:"to"`
			InputType string `json:"inputType"`
		} `json:"connections"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &doc); err != nil {
			return fmt.Sprintf("画布快照解析失败：%v", err), nil
		}
	}
	out := struct {
		Nodes       []readCanvasNode `json:"nodes"`
		Connections []readCanvasConn `json:"connections"`
	}{Nodes: []readCanvasNode{}, Connections: []readCanvasConn{}}
	for _, n := range doc.Nodes {
		var s struct {
			Name        string `json:"name"`
			Prompt      string `json:"prompt"`
			VideoPrompt string `json:"videoPrompt"`
			Text        string `json:"text"`
		}
		_ = json.Unmarshal(n.Settings, &s)
		// 提示词按真实字段名回显进 prompt 字段统一展示；改写时 update_node 的
		// 工具描述会教 planner 用对字段名（gen-video 是 videoPrompt）。
		prompt := s.Prompt
		if n.Type == "gen-video" && s.VideoPrompt != "" {
			prompt = s.VideoPrompt
		}
		out.Nodes = append(out.Nodes, readCanvasNode{
			ID: n.ID, Type: n.Type, Name: s.Name,
			Prompt: truncate(prompt, 200), Text: truncate(s.Text, 100),
			HasMedia: n.Content != "", Status: n.Status,
			Settings: visibleScalarSettings(n.Type, n.Settings),
			X:        n.X, Y: n.Y, W: n.Width, H: n.Height,
		})
	}
	for _, c := range doc.Connections {
		out.Connections = append(out.Connections, readCanvasConn{From: c.From, To: c.To, InputType: c.InputType})
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// ---- 写操作公共骨架 ----

// emitCanvasOp 把一个画布操作打成 artifact 发给前端执行。name 给聊天气泡的
// 操作 chip 用（人话描述），payload 是前端执行所需的结构化参数。
func emitCanvasOp(ctx context.Context, artifactType, name string, payload any) {
	b, _ := json.Marshal(payload)
	EmitFromContext(ctx)(Artifact{
		ArtifactType: artifactType,
		ArtifactID:   uuid.NewString(),
		Name:         name,
		Payload:      b,
	})
}

// snapshotNodeName 在操作执行那一刻从服务端快照取节点显示名（settings.name，
// trim 后非空才算数）。为什么必须在执行时取：删除类 chip 渲染时节点已经不在
// 画布上，前端「渲染时 id→名称」查无可查——名字只能趁节点还活着时定格进
// artifact payload 随消息落库。所有引用节点的操作统一走这里（规则：
// 有名称显示名称，没名称才显示 id，且以后新增操作自动继承）。
// 查不到（无 Reader / 快照缺失 / 节点不存在 / 没起名）返回 ""，前端按
// 「画布现查 → 定格名 → id」链条兜底。
func snapshotNodeName(ctx context.Context, reader CanvasSnapshotReader, nodeID string) string {
	projectID := ProjectIDFromContext(ctx)
	if reader == nil || projectID == 0 || nodeID == "" {
		return ""
	}
	payload, err := reader.GetLatestSnapshot(projectID)
	if err != nil || len(payload) == 0 {
		return ""
	}
	var doc struct {
		Nodes []struct {
			ID       string `json:"id"`
			Settings struct {
				Name string `json:"name"`
			} `json:"settings"`
		} `json:"nodes"`
	}
	if json.Unmarshal(payload, &doc) != nil {
		return ""
	}
	for _, n := range doc.Nodes {
		if n.ID == nodeID {
			return strings.TrimSpace(n.Settings.Name)
		}
	}
	return ""
}

// stripMachineFields 过滤掉 agent 不可写的 settings 字段，返回被剥掉的字段名。
func stripMachineFields(settings map[string]json.RawMessage) []string {
	var stripped []string
	for k := range settings {
		if machineManagedSettings[k] {
			delete(settings, k)
			stripped = append(stripped, k)
		}
	}
	return stripped
}

func strippedNote(stripped []string) string {
	if len(stripped) == 0 {
		return ""
	}
	return fmt.Sprintf("（字段 %s 由系统管理，已忽略。）", strings.Join(stripped, "、"))
}

// ---- create_node ----

// CreateNodeTool 建节点。Catalog 是给能力清单校验用的——新建 gen 节点时若带了
// modelId，动态字段（比例/分辨率）的合法值域要按那个模型判定。
type CreateNodeTool struct {
	Catalog CanvasModelCatalog
}

func (t *CreateNodeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: CreateNodeToolName,
		Desc: fmt.Sprintf("Create a canvas node of any type (%s) with initial settings and optional position. Unknown or non-writable settings field names are REJECTED with an error listing what is allowed. Prompt field names: gen-image uses settings.prompt, gen-video uses settings.videoPrompt, text-node uses settings.text. To set a model, take modelId from %s (display names are not valid values). Never triggers generation.", strings.Join(knownNodeTypes, ", "), ListCanvasModelsToolName),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"type":     {Type: schema.String, Desc: "Node type, one of: " + strings.Join(knownNodeTypes, ", "), Required: true},
			"settings": {Type: schema.String, Desc: "JSON object of initial settings (e.g. {\"prompt\":\"...\",\"name\":\"镜头1\"}). Optional."},
			"x":        {Type: schema.Number, Desc: "Optional canvas x coordinate."},
			"y":        {Type: schema.Number, Desc: "Optional canvas y coordinate."},
		}),
	}, nil
}

func (t *CreateNodeTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Type     string   `json:"type"`
		Settings string   `json:"settings"`
		X        *float64 `json:"x"`
		Y        *float64 `json:"y"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", CreateNodeToolName, err)
	}
	if !isKnownNodeType(args.Type) {
		return "", fmt.Errorf("%s: unknown node type %q, available: %s", CreateNodeToolName, args.Type, strings.Join(knownNodeTypes, ", "))
	}
	settings := map[string]json.RawMessage{}
	if strings.TrimSpace(args.Settings) != "" {
		if err := json.Unmarshal([]byte(args.Settings), &settings); err != nil {
			return "", fmt.Errorf("%s: settings must be a JSON object: %w", CreateNodeToolName, err)
		}
	}
	// 新建节点没有「现值」，本次带的 modelId 就是生效模型。
	var modelID string
	if raw, ok := settings["modelId"]; ok {
		_ = json.Unmarshal(raw, &modelID)
	}
	v := validateNodeSettings(t.Catalog, args.Type, settings, modelID)
	if len(v.Errors) > 0 {
		return rejectionMessage(CreateNodeToolName, v.Errors), nil
	}
	emitCanvasOp(ctx, "node_create_info", args.Type, map[string]any{
		"type": args.Type, "settings": settings, "x": args.X, "y": args.Y,
	})
	return fmt.Sprintf("已提交创建 %s 节点。%s%s", args.Type, strippedNote(v.Stripped), opSubmittedSuffix), nil
}

// ---- update_node ----

// UpdateNodeTool 改节点。它需要 Reader 是因为按能力清单校验得先知道目标节点是
// 什么类型——工具入参只有 node_id。Catalog 用来判定动态字段的合法值域。
type UpdateNodeTool struct {
	Reader  CanvasSnapshotReader
	Catalog CanvasModelCatalog
}

func (t *UpdateNodeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: UpdateNodeToolName,
		Desc: "Update an existing canvas node: merge settings fields and/or move/resize it. Get node ids from read_canvas or user @-mentions. Unknown or non-writable field names are REJECTED with an error listing what is allowed — call " + DescribeCanvasNodeToolName + " when unsure. Prompt field names: gen-image uses settings.prompt, gen-video uses settings.videoPrompt, text-node uses settings.text. To change a model, take modelId from " + ListCanvasModelsToolName + " (display names are not valid values). Never triggers generation.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"node_id":  {Type: schema.String, Desc: "Target node id.", Required: true},
			"settings": {Type: schema.String, Desc: "JSON object of settings fields to merge (e.g. {\"videoPrompt\":\"...\"}). Optional."},
			"x":        {Type: schema.Number, Desc: "Optional new x."},
			"y":        {Type: schema.Number, Desc: "Optional new y."},
			"width":    {Type: schema.Number, Desc: "Optional new width."},
			"height":   {Type: schema.Number, Desc: "Optional new height."},
		}),
	}, nil
}

func (t *UpdateNodeTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		NodeID   string   `json:"node_id"`
		Settings string   `json:"settings"`
		X        *float64 `json:"x"`
		Y        *float64 `json:"y"`
		Width    *float64 `json:"width"`
		Height   *float64 `json:"height"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", UpdateNodeToolName, err)
	}
	if strings.TrimSpace(args.NodeID) == "" {
		return "", fmt.Errorf("%s: node_id is required", UpdateNodeToolName)
	}
	settings := map[string]json.RawMessage{}
	if strings.TrimSpace(args.Settings) != "" {
		if err := json.Unmarshal([]byte(args.Settings), &settings); err != nil {
			return "", fmt.Errorf("%s: settings must be a JSON object: %w", UpdateNodeToolName, err)
		}
	}
	nodeType, curModelID := t.nodeContext(ctx, args.NodeID)
	// 生效模型 = 本次改了就用新的，没改就用节点现值。动态字段的合法性挂在它
	// 身上：同一个 resolution 字段，换个模型允许的档位就不一样。
	effectiveModelID := curModelID
	if raw, ok := settings["modelId"]; ok {
		var mid string
		if json.Unmarshal(raw, &mid) == nil && mid != "" {
			effectiveModelID = mid
		}
	}
	v := validateNodeSettings(t.Catalog, nodeType, settings, effectiveModelID)
	if len(v.Errors) > 0 {
		return rejectionMessage(UpdateNodeToolName, v.Errors), nil
	}
	stripped := v.Stripped
	if len(settings) == 0 && args.X == nil && args.Y == nil && args.Width == nil && args.Height == nil {
		return fmt.Sprintf("%s没有可应用的修改。", strippedNote(stripped)), nil
	}
	emitCanvasOp(ctx, "node_update_info", args.NodeID, map[string]any{
		"node_id": args.NodeID, "node_name": snapshotNodeName(ctx, t.Reader, args.NodeID),
		"settings": settings,
		"x":        args.X, "y": args.Y, "width": args.Width, "height": args.Height,
	})
	return fmt.Sprintf("已提交修改节点 %s。%s%s", args.NodeID, strippedNote(stripped), opSubmittedSuffix), nil
}

// ---- delete_node ----

type DeleteNodeTool struct {
	Reader CanvasSnapshotReader
}

func (t *DeleteNodeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: DeleteNodeToolName,
		Desc: "Delete a canvas node (its connections are removed with it, same as manual deletion; user can undo). Only delete when the user clearly asked for it.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"node_id": {Type: schema.String, Desc: "Target node id.", Required: true},
		}),
	}, nil
}

func (t *DeleteNodeTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", DeleteNodeToolName, err)
	}
	if strings.TrimSpace(args.NodeID) == "" {
		return "", fmt.Errorf("%s: node_id is required", DeleteNodeToolName)
	}
	emitCanvasOp(ctx, "node_delete_info", args.NodeID, map[string]any{
		"node_id": args.NodeID, "node_name": snapshotNodeName(ctx, t.Reader, args.NodeID),
	})
	return fmt.Sprintf("已提交删除节点 %s。%s", args.NodeID, opSubmittedSuffix), nil
}

// ---- connect_nodes / remove_connection ----

type ConnectNodesTool struct {
	Reader CanvasSnapshotReader
}

func (t *ConnectNodesTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: ConnectNodesToolName,
		Desc: "Connect two canvas nodes (from → to), same as the user dragging a wire. E.g. wire an input-image into a gen-video so generation uses it as reference.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"from_node_id": {Type: schema.String, Desc: "Source node id.", Required: true},
			"to_node_id":   {Type: schema.String, Desc: "Target node id.", Required: true},
		}),
	}, nil
}

func (t *ConnectNodesTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		From string `json:"from_node_id"`
		To   string `json:"to_node_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", ConnectNodesToolName, err)
	}
	if args.From == "" || args.To == "" {
		return "", fmt.Errorf("%s: from_node_id and to_node_id are required", ConnectNodesToolName)
	}
	if args.From == args.To {
		return "", fmt.Errorf("%s: cannot connect a node to itself", ConnectNodesToolName)
	}
	emitCanvasOp(ctx, "connection_create_info", fmt.Sprintf("%s → %s", args.From, args.To), map[string]any{
		"from": args.From, "to": args.To,
		"from_name": snapshotNodeName(ctx, t.Reader, args.From),
		"to_name":   snapshotNodeName(ctx, t.Reader, args.To),
	})
	return fmt.Sprintf("已提交连线 %s → %s。%s", args.From, args.To, opSubmittedSuffix), nil
}

type RemoveConnectionTool struct {
	Reader CanvasSnapshotReader
}

func (t *RemoveConnectionTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: RemoveConnectionToolName,
		Desc: "Remove the connection between two canvas nodes (from → to).",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"from_node_id": {Type: schema.String, Desc: "Source node id.", Required: true},
			"to_node_id":   {Type: schema.String, Desc: "Target node id.", Required: true},
		}),
	}, nil
}

func (t *RemoveConnectionTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		From string `json:"from_node_id"`
		To   string `json:"to_node_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", RemoveConnectionToolName, err)
	}
	if args.From == "" || args.To == "" {
		return "", fmt.Errorf("%s: from_node_id and to_node_id are required", RemoveConnectionToolName)
	}
	emitCanvasOp(ctx, "connection_delete_info", fmt.Sprintf("%s ✕ %s", args.From, args.To), map[string]any{
		"from": args.From, "to": args.To,
		"from_name": snapshotNodeName(ctx, t.Reader, args.From),
		"to_name":   snapshotNodeName(ctx, t.Reader, args.To),
	})
	return fmt.Sprintf("已提交断开连线 %s → %s。%s", args.From, args.To, opSubmittedSuffix), nil
}

// ---- 能力清单过滤 ----

// visibleScalarSettings 从节点 settings 里挑出「agent 读得到的标量字段」，供
// read_canvas 的列表视图使用。
//
// 两道过滤缺一不可：
//   - 能力清单（canvasmanifest）挡掉 machine 字段——回传内部运行时状态只会让
//     planner 把它当可操作对象，白吃上下文还诱发误写
//   - 标量判定挡掉数组/对象——它们是详情层的事，在列表里展开会把 200 节点画布
//     的输出撑到不可用
//
// 清单里没有的字段一律不回传：存量快照里可能躺着早已废弃的字段，回传它们等于
// 邀请 agent 去写一个没人读的字段（正是本次要根治的那类静默失败）。
func visibleScalarSettings(nodeType string, raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil
	}
	// 有动态字段的类型（gen-*）放行清单外的标量：ratio / resolution / duration
	// 这些由所选模型的 uiSchema 定义，静态清单里刻意没有它们，但它们的当前值
	// 对列表层判断很有用（「哪些节点比例不对」）。读放宽、写严格——写这些字段
	// 时仍要求它在所选模型的 uiSchema 里，不在就报错。
	allowUnlisted := canvasmanifest.HasDynamicFields(nodeType)
	out := map[string]any{}
	for name, rv := range all {
		spec, ok := canvasmanifest.Field(nodeType, name)
		switch {
		case ok && spec.Access == canvasmanifest.AccessMachine:
			continue
		case !ok && !allowUnlisted:
			continue
		}
		v, ok := decodeScalar(rv)
		if !ok {
			continue
		}
		if sv, isStr := v.(string); isStr {
			if sv == "" {
				continue
			}
			v = truncate(sv, 200)
		}
		out[name] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// decodeScalar 只认 string / number / bool。null、数组、对象都返回 false——
// 前者没有信息量，后两者归详情层。
func decodeScalar(rv json.RawMessage) (any, bool) {
	var v any
	if err := json.Unmarshal(rv, &v); err != nil {
		return nil, false
	}
	switch v.(type) {
	case string, float64, bool:
		return v, true
	default:
		return nil, false
	}
}

// nodeContext 从最新快照取目标节点的类型与当前 modelId。读不到时返回空串——
// 校验会退回「未知类型不拦」的旧行为，而不是把一次合法修改误判为非法：快照
// 短暂读不到（刚建的节点还没推上来）不该让 agent 收到「没有这个字段」的错误。
func (t *UpdateNodeTool) nodeContext(ctx context.Context, nodeID string) (nodeType, modelID string) {
	projectID := ProjectIDFromContext(ctx)
	if t.Reader == nil || projectID == 0 {
		return "", ""
	}
	payload, err := t.Reader.GetLatestSnapshot(projectID)
	if err != nil || len(payload) == 0 {
		return "", ""
	}
	var doc struct {
		Nodes []struct {
			ID       string          `json:"id"`
			Type     string          `json:"type"`
			Settings json.RawMessage `json:"settings"`
		} `json:"nodes"`
	}
	if json.Unmarshal(payload, &doc) != nil {
		return "", ""
	}
	for _, n := range doc.Nodes {
		if n.ID != nodeID {
			continue
		}
		var s struct {
			ModelID string `json:"modelId"`
		}
		_ = json.Unmarshal(n.Settings, &s)
		return n.Type, s.ModelID
	}
	return "", ""
}

// rejectionMessage 把校验错误组织成一条给 agent 看的回执。
//
// 措辞是刻意的：明确说「没有执行」，并把每条问题连同「你能写什么」一起给出。
// 这个工具从前对非法字段静默成功，planner 拿到「已提交修改」后就不再重试了——
// 静默失败对 agent 是最坏的失败，它没有任何信号可以据以自我纠正。
func rejectionMessage(toolName string, errs []string) string {
	return fmt.Sprintf("%s 未执行，请修正后重试：\n- %s", toolName, strings.Join(errs, "\n- "))
}
