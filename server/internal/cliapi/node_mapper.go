package cliapi

import "encoding/json"

// node_mapper 把画布内部 CanvasNode JSON 翻译成 CLI 的稳定语义 DTO。
//
// 这层翻译是 CLI 与内部结构解耦的唯一位置。画布节点类型仍在增加
// （subtitle-erase 刚上测试环境、生产还没有），settings.history 正处在
// 「读时升级」的双格式共存期。CLI 一旦贴着内部结构写，每次画布演进都要
// 重发二进制。
//
// 两条硬规则：
//
//  1. 未知内部类型**原样透传并标 writable=false**，绝不报错、绝不丢弃。
//     类型清单不是封闭枚举。
//  2. NormalizeHistory 与前端 editor/utils/genHistory.ts、后端
//     canvasgen/writeback.go 的同名函数**必须逐字同构**——三者算出的条目数
//     不一致，会让「关页面 / 不关页面 / 走 CLI」看到不同的历史。

// gen-video 的提示词在画布里存 settings.videoPrompt，**不是** settings.prompt。
//
// 这是浏览器定的既有格式：GenVideoNode.tsx 读写的都是 videoPrompt，生产里所有
// 网页/skill 建的视频节点也都是它。CLI 曾经直写 prompt，结果同一个节点两边各读
// 各的字段——CLI 建的节点在网页上提示词一片空白、点生成拿到空 prompt 悄悄回退到
// 上游文本；反过来网页建的节点 CLI 这边也读不到，node run 同样跑空提示词。
// gen-image 两边都是 prompt，所以只有视频中招。
//
// 对 CLI 用户暴露的**始终是 prompt**：那是模型 inputSchema 里的入参名
// （tvmao model get 显示的就是它），画布内部这个历史包袱不该漏给调用方。
// 翻译只发生在这一层，进出各一次。
const (
	videoPromptSettingKey = "videoPrompt"
	promptParamKey        = "prompt"
)

// settingKeyFor 把 CLI 参数名翻成画布 settings 的键名（写入方向）。
func settingKeyFor(internalType, paramKey string) string {
	if internalType == "gen-video" && paramKey == promptParamKey {
		return videoPromptSettingKey
	}
	return paramKey
}

// paramKeyFor 把画布 settings 的键名翻回 CLI 参数名（读出方向）。
func paramKeyFor(internalType, settingKey string) string {
	if internalType == "gen-video" && settingKey == videoPromptSettingKey {
		return promptParamKey
	}
	return settingKey
}

// settingsFromParams / paramsFromSettings 是上面两个函数的批量版本，
// 所有进出画布的 params 都必须过一遍，别在别处手写映射。
func settingsFromParams(internalType string, params map[string]any) map[string]any {
	out := make(map[string]any, len(params))
	for k, v := range params {
		out[settingKeyFor(internalType, k)] = v
	}
	return out
}

func paramsFromSettings(internalType string, settings map[string]any) map[string]any {
	out := make(map[string]any, len(settings))
	for k, v := range settings {
		if internalSettingKeys[k] {
			continue
		}
		out[paramKeyFor(internalType, k)] = v
	}
	// 两个键并存时必须确定性地选一个。存量里有一批 CLI 早期写的 gen-video 节点
	// 带裸 prompt，网页又可能在同一节点上写过 videoPrompt——上面那个循环会把
	// 两者都映射到 params.prompt，谁赢取决于 map 遍历顺序，同一个节点每次读出来
	// 都可能不一样。videoPrompt 是网页认的那个，它说了算。
	if internalType == "gen-video" {
		if vp, ok := settings[videoPromptSettingKey]; ok {
			out[promptParamKey] = vp
		}
	}
	return out
}

// 语义类型 ↔ 内部类型。只有这 4 种 CLI 可写。
//
// 其余类型只读，因为它们要么是 Agent 专属产物（CLI 去创建没有真实场景），
// 要么生产存量是个位数。preview 虽然是节点数第一名（生产 1829 个，占 55%），
// 但它是 Agent 产出容器与占位符，由 CLI 创建同样没有场景。
var writableTypes = []struct {
	semantic string
	internal string
}{
	{"image-input", "input-image"},
	{"audio-input", "input-audio"},
	// 视频输入的内部类型是 video-input（跟 input-image/input-audio 命名方向
	// 相反，前端历史遗留），语义名与内部名恰好同串。上传视频建输入节点走它；
	// canvasinputs 的 videoOutputNodeTypes 白名单里它早就在，下游 gen-video
	// 连上就能拿到 videos 输入。
	{"video-input", "video-input"},
	{"image-generator", "gen-image"},
	{"video-generator", "gen-video"},
}

var (
	semanticByInternal = map[string]string{}
	internalBySemantic = map[string]string{}
)

func init() {
	for _, p := range writableTypes {
		semanticByInternal[p.internal] = p.semantic
		internalBySemantic[p.semantic] = p.internal
	}
}

// WritableSemanticTypes 返回 CLI 可写的语义类型，顺序稳定（用于 help 与校验消息）。
func WritableSemanticTypes() []string {
	out := make([]string, 0, len(writableTypes))
	for _, p := range writableTypes {
		out = append(out, p.semantic)
	}
	return out
}

// SemanticType 把内部类型翻成语义类型，并给出是否可写。
// 未知类型原样返回、标不可写——这是「不把类型清单当封闭枚举」的落点。
func SemanticType(internal string) (string, bool) {
	if sem, ok := semanticByInternal[internal]; ok {
		return sem, true
	}
	return internal, false
}

// InternalType 把语义类型翻回内部类型。只接受 4 种可写语义类型：
// 内部类型名（gen-image 等）不能当语义类型用，否则契约会同时接受两套命名。
func InternalType(semantic string) (string, bool) {
	internal, ok := internalBySemantic[semantic]
	return internal, ok
}

// internalSettingKeys 是不暴露给 CLI 的内部管理键。
//
// 它们不是「用户设置的生成参数」而是画布/后端的内部状态：history 与
// historyIndex 由归一化后单独给出；pendingGen 是提交瞬间的参数快照，由 sink 消费；
// error 提升到 Node.Error；kind/assetId 提升到 Node.Media。
var internalSettingKeys = map[string]bool{
	"history":      true,
	"historyIndex": true,
	"pendingGen":   true,
	"error":        true,
	"kind":         true,
	"assetId":      true,
}

// NormalizeHistory 把三种并存的 history 形态统一成 []HistoryItem。
//
// 与 canvasgen/writeback.go 的 normalizeHistory 逐字同构，包括
// 「没有 history 但有 content 的更早期节点，按只有一条、用 content 初始化」这条。
func NormalizeHistory(raw any, fallbackContent any) []HistoryItem {
	items := []HistoryItem{}
	if list, ok := raw.([]any); ok {
		for _, entry := range list {
			switch v := entry.(type) {
			case string:
				if v != "" {
					items = append(items, HistoryItem{URL: v})
				}
			case map[string]any:
				url, _ := v["url"].(string)
				if url == "" {
					continue
				}
				item := HistoryItem{URL: url}
				item.Prompt, _ = v["prompt"].(string)
				if p, ok := v["params"].(map[string]any); ok {
					item.Params = p
				}
				if at, ok := v["at"].(float64); ok {
					item.At = int64(at)
				}
				items = append(items, item)
			}
		}
	}
	if len(items) == 0 {
		if url, ok := fallbackContent.(string); ok && url != "" {
			return []HistoryItem{{URL: url}}
		}
	}
	return items
}

// ToNode 把内部节点 map 翻成语义 DTO。sign 为 nil 时不签名（单测用）；
// 生产必须传 MediaSigner.Sign —— CLI 永远拿不到裸对象存储 URL。
func ToNode(raw map[string]any, sign func(string) string) Node {
	if sign == nil {
		sign = func(s string) string { return s }
	}
	internal, _ := raw["type"].(string)
	semantic, writable := SemanticType(internal)

	n := Node{
		ID:           str(raw["id"]),
		Type:         semantic,
		InternalType: internal,
		Writable:     writable,
		Position:     Position{X: num(raw["x"]), Y: num(raw["y"])},
		Size:         Size{Width: num(raw["width"]), Height: num(raw["height"])},
		Status:       str(raw["status"]),
		Error:        str(raw["error"]),
		TaskID:       int64(num(raw["taskId"])),
		Params:       map[string]any{},
	}
	if c := str(raw["content"]); c != "" {
		n.Content = sign(c)
	}

	settings, _ := raw["settings"].(map[string]any)
	n.Params = paramsFromSettings(internal, settings)
	// settings.error 是画布约定的失败原因位置（见 canvasgen/writeback.go
	// 的 setNodeError），顶层 error 只是部分节点会写。两处都看，settings 优先。
	if e := str(settings["error"]); e != "" {
		n.Error = e
	}

	kind := str(settings["kind"])
	assetID := int64(num(settings["assetId"]))
	if kind != "" || assetID != 0 {
		n.Media = &NodeMedia{Kind: kind, AssetID: assetID}
	}

	n.History = NormalizeHistory(settings["history"], raw["content"])
	for i := range n.History {
		n.History[i].URL = sign(n.History[i].URL)
	}
	return n
}

// ToEdge 把内部连线 map 翻成 DTO。
func ToEdge(raw map[string]any) Edge {
	return Edge{
		ID:         str(raw["id"]),
		FromNodeID: str(raw["from"]),
		ToNodeID:   str(raw["to"]),
		InputType:  str(raw["inputType"]),
	}
}

// canvasDoc 是快照的最小解析视图。用 json.RawMessage 之外的字段一律不碰——
// 本包只读，写入路径走 canvasstate 的外科式 mutation，那边会逐字保留未知字段。
type canvasDoc struct {
	Nodes       []map[string]any `json:"nodes"`
	Connections []map[string]any `json:"connections"`
}

// ParseCanvas 解析快照 payload。
//
// 空 payload（新项目还没存过）回空画布；**损坏的 payload 必须报错**——
// 静默当成空画布会让用户以为自己的画布没了。
func ParseCanvas(payload json.RawMessage, version int64, sign func(string) string) (Canvas, error) {
	c := Canvas{Version: version, Nodes: []Node{}, Edges: []Edge{}}
	if len(payload) == 0 || string(payload) == "null" {
		return c, nil
	}
	var doc canvasDoc
	if err := json.Unmarshal(payload, &doc); err != nil {
		return c, New(CodeInternal, "画布快照损坏，无法解析").WithCause(err)
	}
	for _, raw := range doc.Nodes {
		c.Nodes = append(c.Nodes, ToNode(raw, sign))
	}
	// 连线顺序即输入顺序契约，遍历时不得重排。
	for _, raw := range doc.Connections {
		c.Edges = append(c.Edges, ToEdge(raw))
	}
	return c, nil
}

// FindNode 按 id 在快照里定位节点原始 map。
func FindNode(doc map[string]any, nodeID string) (map[string]any, bool) {
	nodes, _ := doc["nodes"].([]any)
	for _, raw := range nodes {
		if n, ok := raw.(map[string]any); ok && str(n["id"]) == nodeID {
			return n, true
		}
	}
	return nil, false
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case int:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	}
	return 0
}
