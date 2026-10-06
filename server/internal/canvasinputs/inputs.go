// Package canvasinputs 是画布「生成入参派生」的服务端实现，与浏览器里的三段
// 逻辑**必须逐字同构**：
//
//	Connected           ↔ apps/web/src/modules/canvas/editor/store.ts        getConnectedInputs
//	BuildDynamicInputs  ↔ apps/web/.../editor/utils/generationInputs.ts      buildDynamicInputs
//	composePrompt       ↔ apps/web/.../editor/utils/generationInputs.ts      composePrompt
//	BuildImageInputs    ↔ apps/web/.../editor/utils/generationInputs.ts      buildImageGenerationInputs
//	BuildVideoInputs    ↔ apps/web/.../editor/utils/generationInputs.ts      buildVideoGenerationInputs
//	CalculateResolution ↔ apps/web/.../editor/utils/resolution.ts            calculateResolution
//
// 为什么是两份而不是一份：现有 POST /api/v1/canvas-tasks 要求调用方传入**已经
// 组装好的** inputs，组装全在浏览器里，Go 后端一行都没有。CLI 没有浏览器，
// 所以服务端必须补一份。顺手把网页也迁过来风险最高——画布结构正处在活跃演进期
// （history 刚改格式、subtitle-erase 刚加），不在移动的靶子上改线上生成路径。
//
// 仓库已有同类先例：canvasgen/writeback.go 的 normalizeHistory 与前端
// genHistory.ts 必须逐字同构，那里的注释记录了不同步的后果——「关页面和不关页面，
// 历史条数不一样」。这是第二个同构对，用 golden 测试锁住。
//
// 验收标准不是「CLI 能跑通」，而是**同一节点、同一参数，CLI 提交与网页提交
// 产出一致**。任一侧改动必须同步改另一侧并更新 inputs_test.go 的 golden。
package canvasinputs

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ConnectedInputs 是某节点的上游输入，按 connections 数组顺序分类。
// *IDs 与同名媒体列表严格同序（第 i 个 URL 来自第 i 个 ID 的节点）——
// 提示词 @ 提及按节点 ID 翻译成「图片N」时，N 就是 ID 在这里的位置。
type ConnectedInputs struct {
	Images []string
	Texts  []string
	Videos []string
	Audios []string

	ImageIDs []string
	VideoIDs []string
	AudioIDs []string
}

// Snapshot 是本包看待画布的最小视图。刻意不依赖 cliapi/projects——
// 这是画布的领域逻辑，不是 CLI 的实现细节；将来网页迁到服务端，调的是同一个包。
type Snapshot struct {
	Nodes       []map[string]any
	Connections []map[string]any
}

// ParseSnapshot 从快照 payload 解析出本包需要的视图。
func ParseSnapshot(payload json.RawMessage) (Snapshot, error) {
	var doc struct {
		Nodes       []map[string]any `json:"nodes"`
		Connections []map[string]any `json:"connections"`
	}
	if len(payload) == 0 || string(payload) == "null" {
		return Snapshot{}, nil
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Nodes: doc.Nodes, Connections: doc.Connections}, nil
}

// videoOutputNodeTypes 是产物类型**固定为视频**的节点类型。
//
// 为什么要显式列举而不是靠 URL 后缀猜：后处理产物的签名 URL 通常没有扩展名
// （对象 key 形如 gen/task-42-0），后缀正则一律猜不中；而 settings.kind 依赖
// 节点成功时写入，旧快照 / agent 产出的节点可能没写。节点类型是三者里唯一
// 始终可靠的真源。
//
// 漏了这个白名单的后果（线上出过）：去字幕产物接给转高清节点，下游报
// 「请连接一个视频」，因为视频被归进了 images。
var videoOutputNodeTypes = map[string]bool{
	"gen-video":        true,
	"video-input":      true,
	"subtitle-erase":   true,
	"video-upscale":    true,
	"video-whitemodel": true,
}

var (
	httpURLRe  = regexp.MustCompile(`^https?://`)
	audioExtRe = regexp.MustCompile(`(?i)\.(mp3|wav|aac|ogg|flac|m4a)(\?|$)`)
	videoExtRe = regexp.MustCompile(`(?i)\.(mp4|webm|mov)(\?|$)`)
)

// Connected 取目标节点的上游输入并分类。
//
// 分支顺序与前端 getConnectedInputs 逐字一致，顺序本身就是契约：
// 音频判断必须在视频之前（否则 input-audio 会被扩展名之外的规则先截走），
// 视频白名单必须在扩展名之前（后处理产物没有扩展名）。
//
// **遍历顺序即输入顺序**：connections 数组的排列决定 images/videos/audios
// 里的先后。生产里入度最高的节点（gen-video，7 条入边 = 4 图 → 2 音 → 1 图）
// 就完全靠数组位置承载顺序，没有任何显式标记。
//
// sign 为 nil 时不签名（单测用）；生产必须传 MediaSigner.Sign。
func Connected(snap Snapshot, nodeID string, sign func(string) string) ConnectedInputs {
	if sign == nil {
		sign = func(s string) string { return s }
	}
	out := ConnectedInputs{
		Images: []string{}, Texts: []string{}, Videos: []string{}, Audios: []string{},
		ImageIDs: []string{}, VideoIDs: []string{}, AudioIDs: []string{},
	}
	byID := make(map[string]map[string]any, len(snap.Nodes))
	for _, n := range snap.Nodes {
		if id, ok := n["id"].(string); ok {
			byID[id] = n
		}
	}

	for _, conn := range snap.Connections {
		if to, _ := conn["to"].(string); to != nodeID {
			continue
		}
		from, _ := conn["from"].(string)
		src, ok := byID[from]
		if !ok {
			continue
		}
		srcType, _ := src["type"].(string)
		settings, _ := src["settings"].(map[string]any)

		if srcType == "text-node" {
			if t, _ := settings["text"].(string); t != "" {
				out.Texts = append(out.Texts, t)
			}
			continue
		}
		rawContent, _ := src["content"].(string)
		// 先 sign 再过滤/分类:快照持久化存 canonical 的 media://key(躲签名 URL
		// 24h 过期),而浏览器 store 加载快照时已把它签成 https——网页那份同构代码
		// 看到的恒是 http。这里若拿 canonical 直接过 http 过滤,media:// 活不到
		// sign 那步,已生成的 gen-image 节点会被整个丢弃(CLI 提交生视频 images
		// 为空的生产事故就是这么来的)。sign 认识 media://,其它字符串原样透传。
		url := sign(rawContent)
		if !httpURLRe.MatchString(url) {
			continue
		}
		// settings.kind（'image' | 'video' | 'audio'）是节点自报的产物类型。preview 是通用
		// 媒体容器必须靠它；后处理节点成功时也会写它。
		declaredKind, _ := settings["kind"].(string)

		switch {
		// gen-audio 产物的签名 URL 无扩展名（对象 key 形如 gen/task-N-0），只靠
		// 扩展名正则会漏进 images——节点类型 / declaredKind 必须先兜住。
		case srcType == "input-audio" || srcType == "gen-audio" || declaredKind == "audio" || audioExtRe.MatchString(url):
			out.Audios = append(out.Audios, url)
			out.AudioIDs = append(out.AudioIDs, from)
		case videoOutputNodeTypes[srcType] || declaredKind == "video" || videoExtRe.MatchString(url):
			out.Videos = append(out.Videos, url)
			out.VideoIDs = append(out.VideoIDs, from)
		default:
			out.Images = append(out.Images, url)
			out.ImageIDs = append(out.ImageIDs, from)
		}
	}
	return out
}

// uiSchemaField 是模型 uiSchema 的单字段形状。
type uiSchemaField struct {
	Label   string `json:"label"`
	Default any    `json:"default"`
	Options []struct {
		Value any    `json:"value"`
		Label string `json:"label"`
	} `json:"options"`
}

// BuildDynamicInputs 遍历模型 uiSchema，节点 settings 有值就用，否则取 schema default。
//
// 与前端 `settings?.[key] ?? field.default ?? ”` 逐字一致：**两级兜底**，
// 连 default 都没有时是空串而不是缺键——缺键会让上游按它自己的默认值跑，
// 那和网页提交的结果就不一样了。
func BuildDynamicInputs(uiSchema json.RawMessage, settings map[string]any) map[string]any {
	inputs := map[string]any{}
	var schema map[string]uiSchemaField
	if len(uiSchema) > 0 {
		_ = json.Unmarshal(uiSchema, &schema)
	}
	for key, field := range schema {
		if v, ok := settings[key]; ok && v != nil {
			inputs[key] = v
			continue
		}
		if field.Default != nil {
			inputs[key] = field.Default
			continue
		}
		inputs[key] = ""
	}
	return inputs
}

// composePrompt 与前端 composePrompt 逐字同构：上游文本节点的词在前、节点自身的词
// 在后，`\n` 分隔——两者是基底 + 修正的互补关系，不是二选一。都空则占位符（上游对空
// prompt 的行为不一致，占位符比空串可预期）。
func composePrompt(prompt, fallback string) string {
	parts := make([]string, 0, 2)
	if f := strings.TrimSpace(fallback); f != "" {
		parts = append(parts, f)
	}
	if p := strings.TrimSpace(prompt); p != "" {
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return "(no prompt)"
	}
	return strings.Join(parts, "\n")
}

// BuildImageInputs 组装图片生成的最终 inputs。
//
// 键的写入顺序等价于前端的 `{prompt, ...dynamic, size, width, height, images, image}`：
// size/width/height 在展开之后，所以**恒覆盖** dynamic 里的同名键。
func BuildImageInputs(prompt, fallbackPrompt string, dynamic map[string]any, images []string) map[string]any {
	ratio := stringOr(dynamic["ratio"], "1:1")
	resolution := stringOr(dynamic["resolution"], "1K")
	res := CalculateResolution(ratio, resolution)

	out := map[string]any{}
	for k, v := range dynamic {
		out[k] = v
	}
	out["prompt"] = composePrompt(prompt, fallbackPrompt)
	out["size"] = res.Str
	out["width"] = res.W
	out["height"] = res.H
	out["images"] = nonNil(images)
	// 前端是 `image: connectedImages[0]`，空数组时值为 undefined —— JSON 里
	// 该键**不存在**。写成 "" 会让上游收到一张空图 URL。
	if len(images) > 0 {
		out["image"] = images[0]
	}
	return out
}

// BuildVideoInputs 组装视频生成的最终 inputs。
func BuildVideoInputs(prompt, fallbackPrompt string, dynamic map[string]any, images, videos, audios []string) map[string]any {
	ratio := stringOr(dynamic["ratio"], "16:9")
	resolution := stringOr(dynamic["resolution"], "720P")
	res := CalculateResolution(ratio, resolution)

	out := map[string]any{}
	for k, v := range dynamic {
		out[k] = v
	}
	out["prompt"] = composePrompt(prompt, fallbackPrompt)
	out["size"] = res.Str
	out["width"] = res.W
	out["height"] = res.H
	out["images"] = nonNil(images)
	out["videos"] = nonNil(videos)
	out["audios"] = nonNil(audios)
	return out
}

func stringOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

// nonNil 保证序列化成 [] 而不是 null —— 前端传的是 []，上游可能不认 null。
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
