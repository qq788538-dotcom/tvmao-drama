package canvasstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrInvalidPayload     = errors.New("invalid canvas payload")
	ErrNodeNotFound       = errors.New("node not found")
	ErrConnectionNotFound = errors.New("connection not found")
)

// NodeGeometryPatch 既是入站请求体的元素，也是 canvas_op 摘要里 positions 的元素。
// json tag 是显式补的：入站解码本来就靠大小写不敏感匹配（行为不变），但出站要
// 保证「调用方没传的字段不出现在帧里」——peer 据此只覆盖真正变了的那几个字段。
type NodeGeometryPatch struct {
	ID     string   `json:"id"`
	X      *float64 `json:"x,omitempty"`
	Y      *float64 `json:"y,omitempty"`
	Width  *float64 `json:"width,omitempty"`
	Height *float64 `json:"height,omitempty"`
}

func UpsertNode(payload json.RawMessage, node json.RawMessage) (json.RawMessage, bool, CanvasOpSummary, error) {
	var summary CanvasOpSummary
	doc, err := parsePayload(payload)
	if err != nil {
		return nil, false, summary, err
	}
	item, err := parseItem(node)
	if err != nil {
		return nil, false, summary, err
	}
	id, err := itemID(item)
	if err != nil {
		return nil, false, summary, err
	}
	// 摘要带**整节点**：这条 op 的语义就是整节点替换，peer 直接以它覆盖同 id 节点
	// 即可得到与服务端一致的结果。用 item（已解析并回写的那份）而不是入参 node，
	// 保证摘要与真正落库的字节同源。
	summary = CanvasOpSummary{Kind: OpUpsertNode, Node: mustMarshalObject(item)}

	index := -1
	for i, existing := range doc.Nodes {
		existingID, err := itemID(existing)
		if err != nil {
			return nil, false, summary, err
		}
		if existingID == id {
			index = i
			break
		}
	}

	if index >= 0 {
		if rawEqual(doc.Nodes[index], item) {
			return marshalPayloadWithOp(doc, false, summary)
		}
		doc.Nodes[index] = item
		return marshalPayloadWithOp(doc, true, summary)
	}

	doc.Nodes = append(doc.Nodes, item)
	return marshalPayloadWithOp(doc, true, summary)
}

func PatchNode(payload json.RawMessage, nodeID string, patch json.RawMessage) (json.RawMessage, bool, CanvasOpSummary, error) {
	var summary CanvasOpSummary
	doc, err := parsePayload(payload)
	if err != nil {
		return nil, false, summary, err
	}
	patchObject, err := parseItem(patch)
	if err != nil {
		return nil, false, summary, err
	}
	delete(patchObject, "id")
	// 摘要带**原始 patch**（已剔除 id），不是合并后的整节点：字段级 LWW 的价值全在
	// 这里——peer 只覆盖 patch 提到的字段，自己正在编辑的其它字段不会被顺带回滚。
	// peer 必须用与 applyMergePatch 完全相同的 RFC 7386 语义（见 CanvasOpSummary 注释）。
	summary = CanvasOpSummary{Kind: OpPatchNode, NodeID: nodeID, Patch: mustMarshalObject(patchObject)}

	for i, node := range doc.Nodes {
		id, err := itemID(node)
		if err != nil {
			return nil, false, summary, err
		}
		if id != nodeID {
			continue
		}

		merged := cloneObject(node)
		if err := applyMergePatch(merged, patchObject); err != nil {
			return nil, false, summary, err
		}
		if rawEqual(node, merged) {
			return marshalPayloadWithOp(doc, false, summary)
		}
		doc.Nodes[i] = merged
		return marshalPayloadWithOp(doc, true, summary)
	}

	return nil, false, summary, ErrNodeNotFound
}

func PatchNodePositions(payload json.RawMessage, patches []NodeGeometryPatch) (json.RawMessage, bool, CanvasOpSummary, error) {
	// 摘要原样带请求里的几何补丁（只含调用方真正传了的字段，nil 的不出现在 JSON 里）
	// ——peer 按同一规则只覆盖这几个字段。
	summary := CanvasOpSummary{Kind: OpPatchPositions, Positions: patches}
	doc, err := parsePayload(payload)
	if err != nil {
		return nil, false, summary, err
	}
	if len(patches) == 0 {
		return marshalPayloadWithOp(doc, false, summary)
	}

	nodeIndexes := make(map[string]int, len(doc.Nodes))
	for i, node := range doc.Nodes {
		id, err := itemID(node)
		if err != nil {
			return nil, false, summary, err
		}
		nodeIndexes[id] = i
	}

	changed := false
	for _, patch := range patches {
		index, ok := nodeIndexes[patch.ID]
		if !ok {
			return nil, false, summary, ErrNodeNotFound
		}
		node := cloneObject(doc.Nodes[index])
		fieldChanged, err := setGeometryField(node, "x", patch.X)
		if err != nil {
			return nil, false, summary, err
		}
		if fieldChanged {
			changed = true
		}
		fieldChanged, err = setGeometryField(node, "y", patch.Y)
		if err != nil {
			return nil, false, summary, err
		}
		if fieldChanged {
			changed = true
		}
		fieldChanged, err = setGeometryField(node, "width", patch.Width)
		if err != nil {
			return nil, false, summary, err
		}
		if fieldChanged {
			changed = true
		}
		fieldChanged, err = setGeometryField(node, "height", patch.Height)
		if err != nil {
			return nil, false, summary, err
		}
		if fieldChanged {
			changed = true
		}
		doc.Nodes[index] = node
	}

	return marshalPayloadWithOp(doc, changed, summary)
}

func DeleteNodes(payload json.RawMessage, ids []string) (json.RawMessage, bool, CanvasOpSummary, error) {
	// 摘要只带节点 id。peer 重放时**还要**自己删掉两端命中这些节点的连线——
	// 服务端下面就是这么做的（级联清理），摘要不重复列出那些连线 id：本地
	// 连线表本来就在手边，按端点过滤一遍比让帧带一串 id 更不容易漏。
	summary := CanvasOpSummary{Kind: OpDeleteNodes, NodeIDs: ids}
	doc, err := parsePayload(payload)
	if err != nil {
		return nil, false, summary, err
	}
	if len(ids) == 0 {
		return marshalPayloadWithOp(doc, false, summary)
	}

	deleteIDs := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		deleteIDs[id] = struct{}{}
	}

	foundIDs := make(map[string]struct{}, len(deleteIDs))
	keptNodes := make([]rawObject, 0, len(doc.Nodes))
	for _, node := range doc.Nodes {
		id, err := itemID(node)
		if err != nil {
			return nil, false, summary, err
		}
		if _, ok := deleteIDs[id]; ok {
			foundIDs[id] = struct{}{}
			continue
		}
		keptNodes = append(keptNodes, node)
	}
	if len(foundIDs) != len(deleteIDs) {
		return nil, false, summary, ErrNodeNotFound
	}

	keptConnections := make([]rawObject, 0, len(doc.Connections))
	for _, connection := range doc.Connections {
		from := optionalString(connection, "from")
		to := optionalString(connection, "to")
		_, fromDeleted := deleteIDs[from]
		_, toDeleted := deleteIDs[to]
		if fromDeleted || toDeleted {
			continue
		}
		keptConnections = append(keptConnections, connection)
	}

	changed := len(keptNodes) != len(doc.Nodes) || len(keptConnections) != len(doc.Connections)
	doc.Nodes = keptNodes
	doc.Connections = keptConnections
	return marshalPayloadWithOp(doc, changed, summary)
}

func UpsertConnection(payload json.RawMessage, connection json.RawMessage) (json.RawMessage, bool, CanvasOpSummary, error) {
	var summary CanvasOpSummary
	doc, err := parsePayload(payload)
	if err != nil {
		return nil, false, summary, err
	}
	item, err := parseItem(connection)
	if err != nil {
		return nil, false, summary, err
	}
	id, err := itemID(item)
	if err != nil {
		return nil, false, summary, err
	}
	// 连线是整体替换（没有字段级 patch 端点），摘要带整条连线。
	summary = CanvasOpSummary{Kind: OpUpsertConnection, Connection: mustMarshalObject(item)}
	from, err := requiredString(item, "from")
	if err != nil {
		return nil, false, summary, err
	}
	to, err := requiredString(item, "to")
	if err != nil {
		return nil, false, summary, err
	}
	nodeIDs, err := nodeIDSet(doc.Nodes)
	if err != nil {
		return nil, false, summary, err
	}
	if _, ok := nodeIDs[from]; !ok {
		return nil, false, summary, ErrNodeNotFound
	}
	if _, ok := nodeIDs[to]; !ok {
		return nil, false, summary, ErrNodeNotFound
	}

	index := -1
	for i, existing := range doc.Connections {
		existingID, err := itemID(existing)
		if err != nil {
			return nil, false, summary, err
		}
		if existingID == id {
			index = i
			break
		}
	}

	if index >= 0 {
		if rawEqual(doc.Connections[index], item) {
			return marshalPayloadWithOp(doc, false, summary)
		}
		doc.Connections[index] = item
		return marshalPayloadWithOp(doc, true, summary)
	}

	doc.Connections = append(doc.Connections, item)
	return marshalPayloadWithOp(doc, true, summary)
}

func DeleteConnection(payload json.RawMessage, connectionID string) (json.RawMessage, bool, CanvasOpSummary, error) {
	summary := CanvasOpSummary{Kind: OpDeleteConnection, ConnectionID: connectionID}
	doc, err := parsePayload(payload)
	if err != nil {
		return nil, false, summary, err
	}

	kept := make([]rawObject, 0, len(doc.Connections))
	found := false
	for _, connection := range doc.Connections {
		id, err := itemID(connection)
		if err != nil {
			return nil, false, summary, err
		}
		if id == connectionID {
			found = true
			continue
		}
		kept = append(kept, connection)
	}
	if !found {
		return nil, false, summary, ErrConnectionNotFound
	}

	doc.Connections = kept
	return marshalPayloadWithOp(doc, true, summary)
}

type rawObject map[string]json.RawMessage

type canvasPayload struct {
	Top         map[string]json.RawMessage
	Nodes       []rawObject
	Connections []rawObject
}

func parsePayload(payload json.RawMessage) (canvasPayload, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return canvasPayload{
			Top:         map[string]json.RawMessage{},
			Nodes:       []rawObject{},
			Connections: []rawObject{},
		}, nil
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(payload, &top); err != nil || top == nil {
		return canvasPayload{}, ErrInvalidPayload
	}

	nodes, err := parseObjectArray(top["nodes"])
	if err != nil {
		return canvasPayload{}, err
	}
	connections, err := parseObjectArray(top["connections"])
	if err != nil {
		return canvasPayload{}, err
	}
	return canvasPayload{Top: top, Nodes: nodes, Connections: connections}, nil
}

func parseObjectArray(raw json.RawMessage) ([]rawObject, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []rawObject{}, nil
	}

	var items []rawObject
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, ErrInvalidPayload
	}
	if items == nil {
		return []rawObject{}, nil
	}
	for _, item := range items {
		if item == nil {
			return nil, ErrInvalidPayload
		}
	}
	return items, nil
}

func parseItem(raw json.RawMessage) (rawObject, error) {
	var item rawObject
	if err := json.Unmarshal(raw, &item); err != nil || item == nil {
		return nil, ErrInvalidPayload
	}
	return item, nil
}

func itemID(item rawObject) (string, error) {
	raw, ok := item["id"]
	if !ok {
		return "", ErrInvalidPayload
	}
	var id string
	if err := json.Unmarshal(raw, &id); err != nil || id == "" {
		return "", ErrInvalidPayload
	}
	return id, nil
}

func requiredString(item rawObject, key string) (string, error) {
	raw, ok := item[key]
	if !ok {
		return "", fmt.Errorf("%w: missing %q", ErrInvalidPayload, key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" {
		return "", fmt.Errorf("%w: invalid %q", ErrInvalidPayload, key)
	}
	return value, nil
}

func nodeIDSet(nodes []rawObject) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		id, err := itemID(node)
		if err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, nil
}

func optionalString(item rawObject, key string) string {
	raw, ok := item[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func setGeometryField(item rawObject, key string, value *float64) (bool, error) {
	if value == nil {
		return false, nil
	}

	raw, err := json.Marshal(*value)
	if err != nil {
		return false, fmt.Errorf("%w: invalid geometry field %q: %v", ErrInvalidPayload, key, err)
	}
	if rawEqualRaw(item[key], raw) {
		return false, nil
	}
	item[key] = raw
	return true, nil
}

// marshalPayloadWithOp 只是把 marshalPayload 的三个返回值加上摘要凑成四个。
// 存在的意义是让六个 op 函数的收尾语句保持一行，不必每处都展开成临时变量。
func marshalPayloadWithOp(doc canvasPayload, changed bool, summary CanvasOpSummary) (json.RawMessage, bool, CanvasOpSummary, error) {
	payload, ch, err := marshalPayload(doc, changed)
	return payload, ch, summary, err
}

func marshalPayload(doc canvasPayload, changed bool) (json.RawMessage, bool, error) {
	nodes, err := json.Marshal(doc.Nodes)
	if err != nil {
		return nil, false, err
	}
	connections, err := json.Marshal(doc.Connections)
	if err != nil {
		return nil, false, err
	}

	out := make(map[string]json.RawMessage, len(doc.Top)+2)
	for key, value := range doc.Top {
		out[key] = cloneRaw(value)
	}
	out["nodes"] = nodes
	out["connections"] = connections

	payload, err := json.Marshal(out)
	if err != nil {
		return nil, false, err
	}
	return payload, changed, nil
}

func cloneObject(item rawObject) rawObject {
	clone := make(rawObject, len(item))
	for key, value := range item {
		clone[key] = cloneRaw(value)
	}
	return clone
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	clone := make(json.RawMessage, len(raw))
	copy(clone, raw)
	return clone
}

// applyMergePatch 把 patch 逐键并进 target，语义是 RFC 7386 JSON Merge Patch：
// **patch 里值为 JSON null 的键 = 从目标里删掉这个键**，其余按 mergeRawValue 递归合并。
//
// 为什么必须是删键而不是「存一个 null」：前端字段级 diff（persistNodeById）用
// 「基线有、现在没有」来表达「这个字段没了」——比如生成结束要清掉 settings.error /
// taskId。JSON 里没有 undefined，只能发 null；如果服务端把 null 原样存下来，快照里
// 就会堆一层 `"error": null` 的僵尸键，而后端自己的回写路径（writeback 的
// clearNodeError）用的是 delete——同一个「清空」两条路径产出两种形状，历史/合规这
// 类按键存在性判断的逻辑迟早踩空。
//
// 存量兼容已核过：Go 侧唯一显式发 null 的地方是 cliapi 提交时的
// `settings.error: nil`（意图就是清错误，与删键一致）；网页侧清字段一律用
// `undefined`（JSON.stringify 直接丢掉该键），从来没有「想存 null」的场景。
func applyMergePatch(target rawObject, patch rawObject) error {
	for key, value := range patch {
		if isJSONNull(value) {
			delete(target, key)
			continue
		}
		nextValue, err := mergeRawValue(target[key], value)
		if err != nil {
			return err
		}
		target[key] = nextValue
	}
	return nil
}

func mergeRawValue(existing json.RawMessage, patch json.RawMessage) (json.RawMessage, error) {
	existingObject, existingOK, err := rawObjectValue(existing)
	if err != nil {
		return nil, err
	}
	patchObject, patchOK, err := rawObjectValue(patch)
	if err != nil {
		return nil, err
	}
	if !existingOK || !patchOK {
		return cloneRaw(patch), nil
	}

	merged := cloneObject(existingObject)
	if err := applyMergePatch(merged, patchObject); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// isJSONNull 判断一段 raw JSON 是不是字面量 null。用 Unmarshal 而不是字符串比较：
// 入参可能带空白（`  null `），字符串比较会漏判。
func isJSONNull(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return value == nil
}

func rawObjectValue(raw json.RawMessage) (rawObject, bool, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false, nil
	}

	var value rawObject
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false, nil
	}
	if value == nil {
		return nil, false, nil
	}
	return value, true, nil
}

func rawEqual(a, b rawObject) bool {
	aJSON, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bJSON, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(aJSON, bJSON)
}

func rawEqualRaw(a, b json.RawMessage) bool {
	var av any
	var bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return len(bytes.TrimSpace(a)) == 0 && len(bytes.TrimSpace(b)) == 0
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return rawValueEqual(av, bv)
}

func rawValueEqual(a, b any) bool {
	aJSON, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bJSON, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(aJSON, bJSON)
}
