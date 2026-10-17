package cliapi

import (
	"crypto/rand"
	"encoding/json"
	"strings"
)

// 服务端构造画布节点。
//
// CLI **不允许提交任意 raw node JSON**：那等于把内部字段和未来的前端重构
// 变成公共 API。CLI 只送语义请求，节点长什么样由这里决定。

// defaultSizes 是各内部节点类型的默认宽高，**镜像**前端
// apps/web/src/modules/canvas/editor/store.ts 的 DEFAULT_SIZES。
// 任一侧改动需同步，否则 CLI 建的节点在网页上大小突兀。
var defaultSizes = map[string]Size{
	"input-image": {Width: 260, Height: 260},
	"input-audio": {Width: 300, Height: 200},
	"video-input": {Width: 580, Height: 460},
	"gen-image":   {Width: 280, Height: 280},
	"gen-video":   {Width: 400, Height: 500},
}

// idAlphabet 与 nanoid 默认字母表同为 URL-safe 集合。
const idAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"

func randomID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 在支持的平台上不会失败；真失败了也不能返回可预测 ID，
		// 直接 panic 比悄悄发一个弱 ID 安全。
		panic("cliapi: crypto/rand unavailable: " + err.Error())
	}
	out := make([]byte, len(buf))
	for i, b := range buf {
		out[i] = idAlphabet[int(b)%len(idAlphabet)]
	}
	return prefix + string(out)
}

// NewNodeID / NewEdgeID 与前端 `n-${nanoid(8)}` / `e-${nanoid(8)}` 同形。
func NewNodeID() string { return randomID("n-") }
func NewEdgeID() string { return randomID("e-") }

type CreateNodeRequest struct {
	Type     string         `json:"type"`
	Position *Position      `json:"position,omitempty"`
	Size     *Size          `json:"size,omitempty"`
	Content  string         `json:"content,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
}

// PatchNodeRequest 是 merge patch：只有出现的字段才会被改。
// 不支持整节点替换——那会用陈旧副本覆盖网页刚保存的字段。
type PatchNodeRequest struct {
	Position *Position      `json:"position,omitempty"`
	Size     *Size          `json:"size,omitempty"`
	Content  *string        `json:"content,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
}

// validateParams 拒绝内部管理键。
//
// 让 CLI 写 history / pendingGen 会直接破坏「读时升级」的历史归一化与
// 生成回写的配对逻辑；写 kind / assetId 则会伪造产物元信息。
func validateParams(params map[string]any) error {
	for k := range params {
		if internalSettingKeys[k] {
			return Errf(CodeInvalidArgument, "参数 %q 由服务端维护，不接受外部写入", k)
		}
	}
	return nil
}

// resolveWritableType 把语义类型翻成内部类型，并区分「不可写」与「不存在」。
func resolveWritableType(semantic string) (string, error) {
	if internal, ok := InternalType(semantic); ok {
		return internal, nil
	}
	// 传的是可写类型的**内部名**（gen-image 等）：类型没错，名字用错了。
	// 直接告诉他正确的语义名，比一句「未知类型」有用。
	if sem, ok := semanticByInternal[semantic]; ok {
		return "", Errf(CodeInvalidNodeType, "节点类型应使用语义名 %q 而不是内部名 %q", sem, semantic)
	}
	if isKnownReadonlyType(semantic) {
		return "", Errf(CodeNodeReadonlyType, "节点类型 %q 只读，CLI 可写的类型是 %s",
			semantic, strings.Join(WritableSemanticTypes(), " / "))
	}
	return "", Errf(CodeInvalidNodeType, "未知节点类型 %q，CLI 可写的类型是 %s",
		semantic, strings.Join(WritableSemanticTypes(), " / "))
}

// knownReadonlyTypes 是当前画布里已存在但 CLI 不可写的类型。
//
// **这不是封闭枚举**，只用于把错误消息说得更准（「只读」而不是「不存在」）。
// 类型清单还在增长，漏了新类型最多是错误码从 NODE_READONLY_TYPE 退化成
// INVALID_NODE_TYPE，不会误放行。
var knownReadonlyTypes = map[string]bool{
	"preview": true, "text-node": true, "image-compare": true,
	"local-save": true, "subtitle-erase": true, "video-upscale": true, "video-whitemodel": true,
	"character-description": true, "novel-input": true, "extract-characters-scenes": true,
	"storyboard-node": true, "scene-description": true,
}

func isKnownReadonlyType(t string) bool { return knownReadonlyTypes[t] }

// BuildNodeJSON 生成可直接交给 canvasstate.UpsertNode 的节点 JSON。
func BuildNodeJSON(req CreateNodeRequest) (json.RawMessage, error) {
	internal, err := resolveWritableType(req.Type)
	if err != nil {
		return nil, err
	}
	if err := validateParams(req.Params); err != nil {
		return nil, err
	}

	// 生成节点必须显式带 modelId 并落盘。
	//
	// 生产里 52% 的成功 gen-image 节点根本没有 modelId——根因是网页只在用户点
	// 模型选择器时才写回，提交生成时不写。那些节点当初用的哪个模型已不可考
	// （只能推断为「当时的 models[0]」，而这个顺序会随后台调 sort_order 变化）。
	// CLI 比网页严格，是为了不再制造新的同类节点。
	if internal == "gen-image" || internal == "gen-video" {
		modelID, _ := req.Params["modelId"].(string)
		if strings.TrimSpace(modelID) == "" {
			return nil, New(CodeModelRequired,
				"生成节点必须显式指定模型（--model），服务端不会替你挑一个")
		}
	}

	size := defaultSizes[internal]
	if req.Size != nil {
		size = *req.Size
	}
	pos := Position{}
	if req.Position != nil {
		pos = *req.Position
	}

	settings := settingsFromParams(internal, req.Params)

	node := map[string]any{
		"id":       NewNodeID(),
		"type":     internal,
		"x":        pos.X,
		"y":        pos.Y,
		"width":    size.Width,
		"height":   size.Height,
		"settings": settings,
		// 未运行节点的状态必须明确是 idle，不能让调用方靠「有没有 URL」猜
		// 它是不是正在生成。
		"status": "idle",
	}
	if req.Content != "" {
		node["content"] = req.Content
		// 带内容的输入节点是「已就绪」，下游取它当输入时要能直接用。
		node["status"] = "succeeded"
	}
	return json.Marshal(node)
}

// BuildNodePatch 生成 merge patch。只序列化请求里出现的字段——
// 多带一个 settings.ratio:null 就会把用户在网页刚设的比例抹掉。
//
// internalType 决定参数名怎么翻成 settings 键名（gen-video 的 prompt 存
// videoPrompt）。它只能从快照里的目标节点读，所以调用点必须在读到节点之后。
func BuildNodePatch(internalType string, req PatchNodeRequest) (json.RawMessage, error) {
	if err := validateParams(req.Params); err != nil {
		return nil, err
	}
	patch := map[string]any{}
	if req.Position != nil {
		patch["x"] = req.Position.X
		patch["y"] = req.Position.Y
	}
	if req.Size != nil {
		patch["width"] = req.Size.Width
		patch["height"] = req.Size.Height
	}
	if req.Content != nil {
		patch["content"] = *req.Content
	}
	if len(req.Params) > 0 {
		patch["settings"] = settingsFromParams(internalType, req.Params)
	}
	if len(patch) == 0 {
		return nil, New(CodeInvalidArgument, "没有要修改的字段")
	}
	return json.Marshal(patch)
}
