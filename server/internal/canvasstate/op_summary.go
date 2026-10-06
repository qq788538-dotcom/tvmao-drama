package canvasstate

import "encoding/json"

// CanvasOpKind 是一次画布写操作的种类，与 REST 端点一一对应。
// 取值用前端惯用的 camelCase（对外是 JSON 里的 kind 字段），与 canvas_op 帧其余
// snake_case 字段并存——kind 是**枚举值**不是字段名，与前端 store 的 action 名对齐
// 比强行 snake_case 更好读。
type CanvasOpKind string

const (
	OpUpsertNode       CanvasOpKind = "upsertNode"
	OpPatchNode        CanvasOpKind = "patchNode"
	OpDeleteNodes      CanvasOpKind = "deleteNodes"
	OpPatchPositions   CanvasOpKind = "patchPositions"
	OpUpsertConnection CanvasOpKind = "upsertConnection"
	OpDeleteConnection CanvasOpKind = "deleteConnection"
)

// CanvasOpSummary 是一次画布写操作的**变更摘要**：足够让另一端在本地重放出同一个
// 结果，而不必回头整拉一次快照。
//
// 为什么必须带数据：一期的 canvas_changed 帧只有 version，观看端收到就整拉快照并
// 重建画布——那会清掉选中、拖拽、文本光标等全部本地交互态。多人同时编辑时每个人
// 每写一笔就把同伴的操作打断一次，功能等于不可用。
//
// 摘要不是 diff：它就是请求本身携带的那份数据（整节点 / 原始 patch / id 列表），
// 服务端不做二次比对——mutate 本来就是「按请求改」，重算一遍 diff 只会引入两套
// 语义不一致的风险。
//
// **摘要只在 changed==true 时有意义**：op 函数为了少写几层分支，会在相等性判断
// 之前就把摘要填好，所以一次 no-op（服务端状态本来就等于目标）返回的摘要是有值的。
// 调用方必须按 changed 决定发不发帧——no-op 不涨 version，带 op 发出去会让接收端
// 的「version==local+1」缺口检测失去意义。
//
// **合并语义（前端 applyRemoteOp 必须逐条同构）**：
//   - upsertNode / upsertConnection：以 node / connection 整体替换同 id 的那一条；
//     没有同 id 的就追加。
//   - patchNode：对 node_id 那个节点，用 patch 做 **RFC 7386 JSON Merge Patch**
//     （服务端 applyMergePatch/mergeRawValue）：双方都是 JSON object 时逐键递归；
//     任意一侧不是 object（标量、数组、缺失）时 patch 值整体替换旧值——即**数组永远
//     整体替换，不做逐元素合并**；**patch 值为 null = 删掉该键**（递归各层同理）。
//     patch 里没提到的键原样保留。patch 的 id 键被服务端剔除，不参与合并。
//   - deleteNodes：删掉 node_ids 里的节点，**并**删掉任一端点命中这些节点的连线。
//   - patchPositions：对每个 positions 条目，只覆盖它真正带了的 x/y/width/height
//     字段（缺失字段不动）。
//   - deleteConnection：删掉 connection_id 那条连线。
//
// 字段用 snake_case，与 agent 帧的其余字段惯例一致。
type CanvasOpSummary struct {
	Kind         CanvasOpKind        `json:"kind"`
	Node         json.RawMessage     `json:"node,omitempty"`
	NodeID       string              `json:"node_id,omitempty"`
	Patch        json.RawMessage     `json:"patch,omitempty"`
	NodeIDs      []string            `json:"node_ids,omitempty"`
	Positions    []NodeGeometryPatch `json:"positions,omitempty"`
	Connection   json.RawMessage     `json:"connection,omitempty"`
	ConnectionID string              `json:"connection_id,omitempty"`
}

// mustMarshalObject 把已解析的 rawObject 变回 JSON。入参来自刚 Unmarshal 成功的
// json.RawMessage，marshal 不可能失败；真出了错就退回 null，让接收端走「没有 op
// 数据」的整拉兜底，而不是让整次写入失败——写已经成功了，广播不该反过来毁掉它。
func mustMarshalObject(item rawObject) json.RawMessage {
	raw, err := json.Marshal(item)
	if err != nil {
		return nil
	}
	return raw
}
