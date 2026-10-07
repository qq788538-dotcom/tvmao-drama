package cliapi

// 本文件是 CLI 的**稳定公共 DTO**。改这里等于改公共契约：
// 字段只增不删，语义只增不改；破坏性变更必须开 /api/v2/cli。
//
// 与画布内部快照结构的关系见 node_mapper.go：内部 JSON 正处在活跃演进期，
// 这层 DTO 就是把演进挡在 CLI 之外的那道墙。

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Size struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// HistoryItem 是归一化后的生成历史条目。
//
// 内部 settings.history 有三种并存形态（纯 URL 字符串数组 / 对象数组 / 空数组），
// 且是「读时升级、非写时迁移」——不存在迁移完成的时点。CLI 永远只看到这一种形状。
type HistoryItem struct {
	URL    string         `json:"url"`
	Prompt string         `json:"prompt,omitempty"`
	Params map[string]any `json:"params,omitempty"`
	At     int64          `json:"at,omitempty"`
}

// NodeMedia 是节点产物的媒体元信息，从内部 settings.kind / settings.assetId 提取。
// 单独成块而不是混进 Params——Params 是「用户可改的生成参数」，这两个是产物属性。
type NodeMedia struct {
	Kind    string `json:"kind,omitempty"`    // image | video | audio
	AssetID int64  `json:"assetId,omitempty"` // 已落库的素材 id，可直接 asset download
}

type Node struct {
	ID string `json:"id"`
	// Type 是语义类型（image-generator 等）。未知内部类型时原样回内部类型。
	Type string `json:"type"`
	// InternalType 永远是画布里真实的 type，给调试与未来兼容用。
	InternalType string `json:"internalType"`
	// Writable=false 的节点 CLI 只能读与引用，不能创建/修改/运行。
	Writable bool     `json:"writable"`
	Position Position `json:"position"`
	Size     Size     `json:"size"`
	Status   string   `json:"status,omitempty"` // idle | uploading | generating | succeeded | failed
	// Content 是已签名的媒体 URL；CLI 永远拿不到裸对象存储地址。
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
	TaskID  int64  `json:"taskId,omitempty"`
	// Params 是可读可写的生成参数（prompt/modelId/ratio/…），已剔除内部管理键。
	Params  map[string]any `json:"params"`
	Media   *NodeMedia     `json:"media,omitempty"`
	History []HistoryItem  `json:"history"`
}

// Edge 是画布有向边，也是下游节点的输入依赖。
//
// **不引入 inputRole / inputIndex**：画布已有顺序契约——connections 数组的排列
// 即输入顺序（生产 646 条连线里只有 1 条写了 inputType，它不能作为分类依据）。
type Edge struct {
	ID         string `json:"id"`
	FromNodeID string `json:"fromNodeId"`
	ToNodeID   string `json:"toNodeId"`
	InputType  string `json:"inputType,omitempty"` // 存量字段，只读透传，CLI 不写
}

// DerivedInputs 是某个生成节点按连线派生出的实际模型输入。
//
// 它是**派生数据不是真源**：真源永远是 connections 数组。暴露它是为了让调用方
// 在花钱运行之前能反向检查「这个节点到底会拿到哪几张图、什么顺序」，
// 而不必自己复刻一遍分类规则（那套规则有 preview 按 kind 判定、
// 后处理产物按类型白名单判定等好几个坑）。
type DerivedInputs struct {
	Images []string `json:"images"`
	Texts  []string `json:"texts"`
	Videos []string `json:"videos"`
	Audios []string `json:"audios"`
}

// NodeDetail 是单节点查询的响应：节点本体 + 运行前审计需要的派生信息。
// 列表接口刻意不带 DerivedInputs——那要为每个节点扫一遍全图。
type NodeDetail struct {
	Node
	// DerivedInputs 只在生成节点上有值；输入节点与只读节点为 nil。
	DerivedInputs *DerivedInputs `json:"derivedInputs,omitempty"`
	// Outputs 是该节点已产出的全部媒体 URL（已签名），按 history 顺序。
	Outputs []string `json:"outputs"`
}

type Canvas struct {
	// Version 是快照版本，可用于 If-Match 显式并发保护。
	Version int64  `json:"version"`
	Nodes   []Node `json:"nodes"`
	Edges   []Edge `json:"edges"`
}

type Project struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	CreatedAt    string `json:"createdAt,omitempty"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
	StorageBytes int64  `json:"storageBytes,omitempty"`
}
