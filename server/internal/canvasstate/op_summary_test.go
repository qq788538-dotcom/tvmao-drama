package canvasstate

import (
	"encoding/json"
	"testing"
)

// 摘要是 canvas_op 帧的全部载荷：它带错或带漏，对端就会静默偏离服务端状态
// （而且没有任何报错——两边各画各的）。六种 op 各钉一条。

func TestUpsertNodeSummaryCarriesWholeNode(t *testing.T) {
	payload := json.RawMessage(`{"nodes":[],"connections":[]}`)
	_, changed, summary, err := UpsertNode(payload, json.RawMessage(`{"id":"n1","type":"text","x":1}`))
	if err != nil || !changed {
		t.Fatalf("UpsertNode changed=%v err=%v", changed, err)
	}
	if summary.Kind != OpUpsertNode {
		t.Fatalf("kind = %q, want upsertNode", summary.Kind)
	}
	var node map[string]any
	if err := json.Unmarshal(summary.Node, &node); err != nil {
		t.Fatalf("摘要里的 node 不是合法 JSON: %v", err)
	}
	if node["id"] != "n1" || node["type"] != "text" {
		t.Fatalf("摘要 node = %v, 必须是落库的那个整节点", node)
	}
}

// patchNode 的摘要必须是**原始 patch**，不是合并后的整节点：字段级 LWW 的全部
// 价值就在于对端只覆盖 patch 提到的字段。顺带钉住服务端剔除了 id（peer 不该把
// 一个 id 字段当成业务字段合并进去）。
func TestPatchNodeSummaryCarriesRawPatchWithoutID(t *testing.T) {
	payload := json.RawMessage(`{"nodes":[{"id":"n1","type":"text","settings":{"a":1,"b":2}}],"connections":[]}`)
	_, changed, summary, err := PatchNode(payload, "n1", json.RawMessage(`{"id":"n1","settings":{"a":9}}`))
	if err != nil || !changed {
		t.Fatalf("PatchNode changed=%v err=%v", changed, err)
	}
	if summary.Kind != OpPatchNode || summary.NodeID != "n1" {
		t.Fatalf("summary = %+v, want kind=patchNode node_id=n1", summary)
	}
	var patch map[string]any
	if err := json.Unmarshal(summary.Patch, &patch); err != nil {
		t.Fatal(err)
	}
	if _, hasID := patch["id"]; hasID {
		t.Fatalf("摘要 patch 不该带 id: %s", summary.Patch)
	}
	settings, ok := patch["settings"].(map[string]any)
	if !ok || settings["a"] != float64(9) {
		t.Fatalf("摘要 patch = %s, want settings.a=9", summary.Patch)
	}
	// 关键：patch 里不该出现没被改的 b——带上它就等于把「未提及字段保持不变」
	// 偷偷变成了整节点覆盖。
	if _, hasB := settings["b"]; hasB {
		t.Fatalf("摘要 patch 混进了未变更字段 b: %s", summary.Patch)
	}
}

func TestDeleteNodesSummaryCarriesIDs(t *testing.T) {
	payload := json.RawMessage(`{"nodes":[{"id":"a"},{"id":"b"}],"connections":[{"id":"c1","from":"a","to":"b"}]}`)
	_, changed, summary, err := DeleteNodes(payload, []string{"b"})
	if err != nil || !changed {
		t.Fatalf("DeleteNodes changed=%v err=%v", changed, err)
	}
	if summary.Kind != OpDeleteNodes || len(summary.NodeIDs) != 1 || summary.NodeIDs[0] != "b" {
		t.Fatalf("summary = %+v, want kind=deleteNodes node_ids=[b]", summary)
	}
}

// positions 摘要必须只带调用方真正传了的字段：没传 y/width 却在帧里冒出 0，
// 对端就会把节点弹回原点。
func TestPatchNodePositionsSummaryOmitsUnsetFields(t *testing.T) {
	x := 12.5
	payload := json.RawMessage(`{"nodes":[{"id":"n1","x":0,"y":0}],"connections":[]}`)
	_, _, summary, err := PatchNodePositions(payload, []NodeGeometryPatch{{ID: "n1", X: &x}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Kind != OpPatchPositions || len(summary.Positions) != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	raw, err := json.Marshal(summary.Positions[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"id":"n1","x":12.5}` {
		t.Fatalf("positions[0] = %s, want 只含 id 与 x", raw)
	}
}

func TestUpsertConnectionSummaryCarriesConnection(t *testing.T) {
	payload := json.RawMessage(`{"nodes":[{"id":"a"},{"id":"b"}],"connections":[]}`)
	_, changed, summary, err := UpsertConnection(payload, json.RawMessage(`{"id":"c1","from":"a","to":"b"}`))
	if err != nil || !changed {
		t.Fatalf("UpsertConnection changed=%v err=%v", changed, err)
	}
	var conn map[string]any
	if summary.Kind != OpUpsertConnection || json.Unmarshal(summary.Connection, &conn) != nil || conn["id"] != "c1" {
		t.Fatalf("summary = %+v, want kind=upsertConnection 且带整条连线", summary)
	}
}

func TestDeleteConnectionSummaryCarriesID(t *testing.T) {
	payload := json.RawMessage(`{"nodes":[{"id":"a"},{"id":"b"}],"connections":[{"id":"c1","from":"a","to":"b"}]}`)
	_, changed, summary, err := DeleteConnection(payload, "c1")
	if err != nil || !changed {
		t.Fatalf("DeleteConnection changed=%v err=%v", changed, err)
	}
	if summary.Kind != OpDeleteConnection || summary.ConnectionID != "c1" {
		t.Fatalf("summary = %+v, want kind=deleteConnection connection_id=c1", summary)
	}
}

// 摘要的 JSON 形状就是对外契约（T2/T3/T4 按它写前端），字段名写歪不会有任何
// 编译期报错，只会让前端读到 undefined。这里钉死 snake_case 与 omitempty。
func TestCanvasOpSummaryJSONShape(t *testing.T) {
	raw, err := json.Marshal(CanvasOpSummary{
		Kind:   OpPatchNode,
		NodeID: "n1",
		Patch:  json.RawMessage(`{"x":1}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"kind":"patchNode","node_id":"n1","patch":{"x":1}}` {
		t.Fatalf("摘要 JSON = %s", raw)
	}
}
