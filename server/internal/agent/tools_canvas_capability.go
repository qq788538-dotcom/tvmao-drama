// Package agent — tools_canvas_capability.go：让 agent 看见画布节点的「能力」。
//
// 在此之前 agent 对画布是盲写：read_canvas 手挑 7 个字段回传，update_node 除三个
// 机器字段外任意 JSON 放行、写错字段名静默成功。线上实测里 planner 想给一批
// gen-video 节点换模型，猜了个 `model` 字段名（真名是 modelId）、把模型显示名
// 当值写了进去，工具回它「已提交修改」——它没有任何信号可以据以自我纠正，最后
// 放弃并把活推回给用户。
//
// 这两个工具补的是那条链路上缺的信息：
//   - list_canvas_models    有哪些模型可选，它们的内部 id 是什么
//   - describe_canvas_node  这个节点现在什么样、我能改哪些字段、每个字段能填什么
//
// 契约本身在 canvasmanifest 包（一份 JSON，浏览器侧有字节级相同的副本）。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/james/huabu_studio/apps/api/internal/agent/canvasmanifest"
)

const (
	ListCanvasModelsToolName   = "list_canvas_models"
	DescribeCanvasNodeToolName = "describe_canvas_node"
)

// CanvasModelInfo 是画布生成节点可选模型的对外视图。刻意只有四个字段：agent
// 需要的是「叫什么、id 是什么、能用在哪类节点、参数怎么填」，上游 basePath /
// 计费配置 / 请求模板与它无关，也不该让它看见。
type CanvasModelInfo struct {
	ModelID  string          `json:"modelId"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	UISchema json.RawMessage `json:"-"`
}

// CanvasModelCatalog 是这两个工具需要的模型目录能力子集。定义窄接口而不是直接
// 依赖 internal/models，与 DBRegistry 的 ModelResolver 同一风格：agent 包不该
// 因为要读个模型名就把整个模型配置层拖进来。
type CanvasModelCatalog interface {
	// ListCanvasModels 返回用户端可见的模型（隐藏/停用的不给——agent 选了也用不了）。
	ListCanvasModels() ([]CanvasModelInfo, error)
	// GetCanvasModel 按 modelId 精确查一个。
	GetCanvasModel(modelID string) (*CanvasModelInfo, bool)
}

// ---- list_canvas_models ----

type ListCanvasModelsTool struct {
	Catalog CanvasModelCatalog
}

func (t *ListCanvasModelsTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: ListCanvasModelsToolName,
		Desc: "List the generation models available for canvas nodes. Returns each model's internal modelId (what you must write into settings.modelId), display name, and type. ALWAYS call this before setting a model on a node — model display names shown in chat are NOT valid values.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"type": {Type: schema.String, Desc: "Optional filter: \"image\" | \"video\" | \"audio\" | \"text\". Omit for all."},
		}),
	}, nil
}

func (t *ListCanvasModelsTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	if t.Catalog == nil {
		return "list_canvas_models 不可用：模型目录未接入。", nil
	}
	var args struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)

	all, err := t.Catalog.ListCanvasModels()
	if err != nil {
		return fmt.Sprintf("读取模型目录失败：%v", err), nil
	}
	want := strings.TrimSpace(strings.ToLower(args.Type))
	out := make([]CanvasModelInfo, 0, len(all))
	for _, m := range all {
		if want != "" && !strings.EqualFold(m.Type, want) {
			continue
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		if want != "" {
			return fmt.Sprintf("没有 type=%s 的可用模型。", want), nil
		}
		return "当前没有可用模型。", nil
	}
	b, _ := json.Marshal(map[string]any{"models": out})
	return string(b), nil
}

// ---- describe_canvas_node ----

type DescribeCanvasNodeTool struct {
	Reader  CanvasSnapshotReader
	Catalog CanvasModelCatalog
}

func (t *DescribeCanvasNodeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: DescribeCanvasNodeToolName,
		Desc: "Describe ONE canvas node in full: its current settings values, exactly which fields you may write, and the allowed values for each. Call this before update_node when you are unsure of a field name or an allowed value — writing an unknown field is an error, not a silent no-op.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"node_id": {Type: schema.String, Desc: "Target node id (from read_canvas or a user @-mention).", Required: true},
		}),
	}, nil
}

// describeField 是一个字段对 agent 的完整说明。
type describeField struct {
	Field   string `json:"field"`
	Type    string `json:"type"`
	Desc    string `json:"desc"`
	Current any    `json:"current,omitempty"`
	// Allowed 非空时，写入必须取其中之一（来自所选模型的 uiSchema）。
	Allowed []any  `json:"allowed,omitempty"`
	Default any    `json:"default,omitempty"`
	Note    string `json:"note,omitempty"`
}

func (t *DescribeCanvasNodeTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	projectID := ProjectIDFromContext(ctx)
	if t.Reader == nil || projectID == 0 {
		return "describe_canvas_node 不可用：画布快照未接入。", nil
	}
	var args struct {
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", DescribeCanvasNodeToolName, err)
	}
	nodeID := strings.TrimSpace(args.NodeID)
	if nodeID == "" {
		return "", fmt.Errorf("%s: node_id is required", DescribeCanvasNodeToolName)
	}

	nodeType, settings, found, err := t.findNode(projectID, nodeID)
	if err != nil {
		return fmt.Sprintf("读取画布失败：%v", err), nil
	}
	if !found {
		return fmt.Sprintf("画布上没有 id 为 %s 的节点。先调 read_canvas 拿真实节点 id。", nodeID), nil
	}
	if !canvasmanifest.KnownNodeType(nodeType) {
		// 未知类型原样报告而不是报错：节点类型清单还在增长，把它当封闭枚举会让
		// 每加一种类型就要重发后端。
		return fmt.Sprintf(`{"node_id":%q,"type":%q,"writable":false,"note":"This node type is not in the capability manifest; it is read-only to agents."}`, nodeID, nodeType), nil
	}

	writable := t.describeWritable(nodeType, settings)
	readonly := t.describeReadonly(nodeType, settings)

	out := map[string]any{
		"node_id":         nodeID,
		"type":            nodeType,
		"writable_fields": writable,
		"readonly_fields": readonly,
	}
	if canvasmanifest.HasDynamicFields(nodeType) {
		out["note"] = "Fields marked with allowed values come from the selected model's schema. Changing modelId resets them to the new model's defaults automatically — you do not need to send them yourself."
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// findNode 从最新快照里取一个节点的类型与 settings 原文。
func (t *DescribeCanvasNodeTool) findNode(projectID int64, nodeID string) (string, map[string]json.RawMessage, bool, error) {
	payload, err := t.Reader.GetLatestSnapshot(projectID)
	if err != nil {
		return "", nil, false, err
	}
	var doc struct {
		Nodes []struct {
			ID       string          `json:"id"`
			Type     string          `json:"type"`
			Settings json.RawMessage `json:"settings"`
		} `json:"nodes"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &doc); err != nil {
			return "", nil, false, err
		}
	}
	for _, n := range doc.Nodes {
		if n.ID != nodeID {
			continue
		}
		settings := map[string]json.RawMessage{}
		if len(n.Settings) > 0 {
			_ = json.Unmarshal(n.Settings, &settings)
		}
		return n.Type, settings, true, nil
	}
	return "", nil, false, nil
}

func (t *DescribeCanvasNodeTool) describeWritable(nodeType string, settings map[string]json.RawMessage) []describeField {
	out := []describeField{}
	for _, name := range canvasmanifest.WritableFields(nodeType) {
		spec, _ := canvasmanifest.Field(nodeType, name)
		f := describeField{Field: name, Type: spec.Type, Desc: spec.Desc}
		if raw, ok := settings[name]; ok {
			// 可写字段给**全文**：agent 要修改它，看不见现值就改不成。
			// script.rows 就是这个分支存在的理由——只报「13 行」的话，agent
			// 想改第 3 镜的时长就只能整表重写，把用户的其余 12 行内容抹掉。
			f.Current = decodeFull(raw)
		}
		switch {
		case spec.WholeReplace && spec.IDManaged:
			f.Note = "Send the whole array. Element ids are managed for you: keep the id of a row you are editing, omit it for a new row."
		case spec.WholeReplace:
			f.Note = "Send the whole array; partial arrays replace the entire value."
		case spec.ResetsDynamicFields:
			f.Note = "Changing this resets the model-dependent fields to the new model's defaults."
		}
		out = append(out, f)
	}
	// 动态字段（比例/分辨率/时长等）的值域随所选模型走，静态清单里没有它们。
	out = append(out, t.dynamicFields(nodeType, settings)...)
	return out
}

func (t *DescribeCanvasNodeTool) describeReadonly(nodeType string, settings map[string]json.RawMessage) []describeField {
	out := []describeField{}
	writable := map[string]bool{}
	for _, n := range canvasmanifest.WritableFields(nodeType) {
		writable[n] = true
	}
	for _, name := range canvasmanifest.VisibleFields(nodeType) {
		if writable[name] {
			continue
		}
		spec, _ := canvasmanifest.Field(nodeType, name)
		f := describeField{Field: name, Type: spec.Type, Desc: spec.Desc}
		if raw, ok := settings[name]; ok {
			if spec.FullValue {
				f.Current = decodeFull(raw)
			} else {
				f.Current = decodeForDescribe(raw)
			}
		}
		out = append(out, f)
	}
	return out
}

// dynamicFields 把所选模型 uiSchema 的每个字段翻成一条带值域的说明。节点没选
// 模型、模型查不到、或该模型没配 uiSchema 时返回空——不是错误，只是无从可说。
func (t *DescribeCanvasNodeTool) dynamicFields(nodeType string, settings map[string]json.RawMessage) []describeField {
	if !canvasmanifest.HasDynamicFields(nodeType) || t.Catalog == nil {
		return nil
	}
	var modelID string
	if raw, ok := settings["modelId"]; ok {
		_ = json.Unmarshal(raw, &modelID)
	}
	if modelID == "" {
		return nil
	}
	m, ok := t.Catalog.GetCanvasModel(modelID)
	if !ok || len(m.UISchema) == 0 {
		return nil
	}
	var ui map[string]struct {
		Label   string `json:"label"`
		Default any    `json:"default"`
		Options []struct {
			Value any    `json:"value"`
			Label string `json:"label"`
		} `json:"options"`
	}
	if err := json.Unmarshal(m.UISchema, &ui); err != nil {
		return nil
	}
	names := make([]string, 0, len(ui))
	for name := range ui {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]describeField, 0, len(names))
	for _, name := range names {
		field := ui[name]
		f := describeField{
			Field:   name,
			Type:    "string|number",
			Desc:    fmt.Sprintf("%s (from model %s)", firstNonEmpty(field.Label, name), m.Name),
			Default: field.Default,
		}
		for _, opt := range field.Options {
			f.Allowed = append(f.Allowed, opt.Value)
		}
		if raw, ok := settings[name]; ok {
			f.Current = decodeForDescribe(raw)
		}
		out = append(out, f)
	}
	return out
}

// decodeFull 解一个可写字段的当前值，数组/对象原样给全。字符串仍然截断——
// 提示词类字段可以很长，而 agent 改提示词是整体重写，不需要逐字比对旧值。
func decodeFull(raw json.RawMessage) any {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	if sv, ok := v.(string); ok {
		return truncate(sv, 2000)
	}
	return v
}

// decodeForDescribe 解一个**只读**字段的当前值。数组和对象只报规模：它们是给
// agent 当上下文的，不是它要改的东西，全文只是噪音（video-input.frames 展开
// 能有上百条）。唯一的例外由调用方决定——可写字段走 decodeFull。
func decodeForDescribe(raw json.RawMessage) any {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	switch tv := v.(type) {
	case []any:
		return fmt.Sprintf("<array of %d>", len(tv))
	case map[string]any:
		return fmt.Sprintf("<object with %d keys>", len(tv))
	case string:
		return truncate(tv, 500)
	default:
		return v
	}
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// ---- 按清单校验写入 ----

// settingsValidation 是一次校验的结果。
type settingsValidation struct {
	// Stripped 是被静默剥掉的机器管理字段。静默而不报错是历史行为，保持不变：
	// 这类字段 agent 本来就看不见（machine 不回传），写进来多半是模型幻觉，
	// 为它报错只会让对话被无意义的纠错占满。
	Stripped []string
	// Errors 非空时整次写入应当被拒。每条是一句给 agent 看的人话，带上「你能
	// 写什么」——只说「字段非法」而不给可用清单，等于让它换个姿势再猜一次。
	Errors []string
}

// validateNodeSettings 按能力清单校验 agent 提交的 settings，就地剥掉机器字段。
//
// effectiveModelID 是这个节点**本次写入之后**的模型 id（本次改了就用新的，没改
// 就用节点现值）。动态字段的合法性挂在它身上：同一个 `resolution` 字段，换个
// 模型允许的档位就不一样。
func validateNodeSettings(catalog CanvasModelCatalog, nodeType string, settings map[string]json.RawMessage, effectiveModelID string) settingsValidation {
	var res settingsValidation
	if !canvasmanifest.KnownNodeType(nodeType) {
		// 未知类型不拦：节点类型清单还在增长，把它当封闭枚举会让每加一种类型
		// 就要重发后端。退回旧行为——只剥机器字段。
		res.Stripped = stripMachineFields(settings)
		return res
	}

	dynamic := dynamicFieldOptions(catalog, nodeType, effectiveModelID)
	writable := map[string]bool{}
	for _, n := range canvasmanifest.WritableFields(nodeType) {
		writable[n] = true
	}

	for name, raw := range settings {
		spec, known := canvasmanifest.Field(nodeType, name)
		switch {
		case known && spec.Access == canvasmanifest.AccessMachine:
			delete(settings, name)
			res.Stripped = append(res.Stripped, name)
		case known && spec.Access == canvasmanifest.AccessReadonly:
			res.Errors = append(res.Errors, fmt.Sprintf(
				"字段 %q 是只读的（%s），不能写。", name, spec.Desc))
		case known:
			// open：放行。
		default:
			// 静态清单里没有——可能是所选模型带来的动态字段。
			opts, isDynamic := dynamic[name]
			if !isDynamic {
				res.Errors = append(res.Errors, fmt.Sprintf(
					"节点类型 %s 没有字段 %q。可写字段：%s。",
					nodeType, name, strings.Join(sortedKeys(writable), "、")))
				continue
			}
			if msg := checkAllowedValue(name, raw, opts); msg != "" {
				res.Errors = append(res.Errors, msg)
			}
		}
	}
	sort.Strings(res.Errors)
	sort.Strings(res.Stripped)
	return res
}

// dynamicFieldOptions 返回该节点在给定模型下的动态字段 → 允许值。模型查不到或
// 没配 uiSchema 时返回空 map：此时动态字段一律按「未知字段」拒绝，因为我们确实
// 无法判断它合不合法，放行等于回到静默失败。
func dynamicFieldOptions(catalog CanvasModelCatalog, nodeType, modelID string) map[string][]any {
	if !canvasmanifest.HasDynamicFields(nodeType) || catalog == nil || modelID == "" {
		return nil
	}
	m, ok := catalog.GetCanvasModel(modelID)
	if !ok || len(m.UISchema) == 0 {
		return nil
	}
	var ui map[string]struct {
		Options []struct {
			Value any `json:"value"`
		} `json:"options"`
	}
	if err := json.Unmarshal(m.UISchema, &ui); err != nil {
		return nil
	}
	out := make(map[string][]any, len(ui))
	for name, field := range ui {
		vals := make([]any, 0, len(field.Options))
		for _, o := range field.Options {
			vals = append(vals, o.Value)
		}
		out[name] = vals
	}
	return out
}

// checkAllowedValue 校验一个动态字段的取值在模型给的选项里。选项为空 = 该字段
// 是自由输入（uiSchema 允许只给 default 不给 options），不拦。
func checkAllowedValue(name string, raw json.RawMessage, allowed []any) string {
	if len(allowed) == 0 {
		return ""
	}
	var got any
	if err := json.Unmarshal(raw, &got); err != nil {
		return fmt.Sprintf("字段 %q 的值不是合法 JSON。", name)
	}
	for _, a := range allowed {
		if sameScalar(a, got) {
			return ""
		}
	}
	labels := make([]string, 0, len(allowed))
	for _, a := range allowed {
		labels = append(labels, fmt.Sprintf("%v", a))
	}
	return fmt.Sprintf("字段 %q 的值 %v 不在当前模型的允许范围内。允许：%s。",
		name, got, strings.Join(labels, "、"))
}

// sameScalar 比较两个来自 JSON 的标量。都经过 encoding/json 解码，数字统一是
// float64，所以 %v 归一后比较是安全的——不会出现 1 与 1.0 打架。
func sameScalar(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
