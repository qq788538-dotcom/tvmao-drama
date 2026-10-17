package canvasstate

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestPatchNodePreservesTopLevelFieldsAndView(t *testing.T) {
	payload := json.RawMessage(`{
		"view": {"x": 10, "y": 20, "zoom": 0.8},
		"unknown": {"keep": true},
		"nodes": [
			{"id": "node-1", "type": "text", "x": 1, "y": 2, "width": 100, "height": 80, "data": {"title": "old"}, "keep": "yes"}
		],
		"connections": []
	}`)
	patch := json.RawMessage(`{"id": "other", "x": 30, "data": {"title": "new"}}`)

	got, changed, _, err := PatchNode(payload, "node-1", patch)
	if err != nil {
		t.Fatalf("PatchNode returned error: %v", err)
	}
	if !changed {
		t.Fatal("PatchNode changed = false, want true")
	}

	doc := decodeObject(t, got)
	if _, ok := doc["view"]; !ok {
		t.Fatal("top-level view was not preserved")
	}
	if _, ok := doc["unknown"]; !ok {
		t.Fatal("top-level unknown field was not preserved")
	}

	node := firstNode(t, doc)
	if node["id"] != "node-1" {
		t.Fatalf("node id = %v, want node-1", node["id"])
	}
	if node["type"] != "text" {
		t.Fatalf("node type = %v, want text", node["type"])
	}
	if node["keep"] != "yes" {
		t.Fatalf("unpatched field keep = %v, want yes", node["keep"])
	}
	if node["x"] != float64(30) {
		t.Fatalf("node x = %v, want 30", node["x"])
	}
	data := node["data"].(map[string]any)
	if data["title"] != "new" {
		t.Fatalf("node data.title = %v, want new", data["title"])
	}
}

func TestPatchMissingNodeReturnsErrNodeNotFound(t *testing.T) {
	payload := json.RawMessage(`{"nodes": [{"id": "node-1"}], "connections": []}`)

	_, changed, _, err := PatchNode(payload, "missing", json.RawMessage(`{"x": 1}`))
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("PatchNode err = %v, want ErrNodeNotFound", err)
	}
	if changed {
		t.Fatal("PatchNode changed = true, want false")
	}
}

func TestPatchNodeRecursivelyMergesNestedObjects(t *testing.T) {
	payload := json.RawMessage(`{
		"nodes": [
			{
				"id": "node-1",
				"settings": {
					"mode": "auto",
					"style": {"color": "red", "size": 12}
				}
			}
		],
		"connections": []
	}`)
	patch := json.RawMessage(`{"settings": {"style": {"color": "blue"}, "error": "x"}}`)

	got, changed, _, err := PatchNode(payload, "node-1", patch)
	if err != nil {
		t.Fatalf("PatchNode returned error: %v", err)
	}
	if !changed {
		t.Fatal("PatchNode changed = false, want true")
	}

	node := firstNode(t, decodeObject(t, got))
	settings := node["settings"].(map[string]any)
	if settings["mode"] != "auto" {
		t.Fatalf("settings.mode = %v, want auto", settings["mode"])
	}
	if settings["error"] != "x" {
		t.Fatalf("settings.error = %v, want x", settings["error"])
	}
	style := settings["style"].(map[string]any)
	if style["color"] != "blue" {
		t.Fatalf("settings.style.color = %v, want blue", style["color"])
	}
	if style["size"] != float64(12) {
		t.Fatalf("settings.style.size = %v, want 12", style["size"])
	}
}

// RFC 7386：patch 值为 null = **删掉这个键**，不是「存一个 null」。
// 前端字段级 diff 用它表达「这个字段没了」（JSON 里没有 undefined），服务端要是
// 原样存下来，快照里就会堆一层 `"error": null` 僵尸键，而后端回写路径用的是 delete
// ——同一个「清空」两条路径产出两种形状。金测试与前端 mergePatch 语义对齐。
func TestPatchNodeNullDeletesKey(t *testing.T) {
	payload := json.RawMessage(`{
		"nodes": [
			{
				"id": "node-1",
				"taskId": 7,
				"settings": {"mode": "auto", "error": "旧错误", "style": {"color": "red", "size": 12}}
			}
		],
		"connections": []
	}`)
	patch := json.RawMessage(`{"taskId": null, "settings": {"error": null, "style": {"size": null}}}`)

	got, changed, _, err := PatchNode(payload, "node-1", patch)
	if err != nil {
		t.Fatalf("PatchNode returned error: %v", err)
	}
	if !changed {
		t.Fatal("PatchNode changed = false, want true")
	}

	node := firstNode(t, decodeObject(t, got))
	if _, ok := node["taskId"]; ok {
		t.Fatalf("顶层 taskId 该被删掉, node = %v", node)
	}
	settings := node["settings"].(map[string]any)
	if _, ok := settings["error"]; ok {
		t.Fatalf("settings.error 该被删掉, settings = %v", settings)
	}
	// 嵌套层同理，且同层未提到的键必须原样保留。
	style := settings["style"].(map[string]any)
	if _, ok := style["size"]; ok {
		t.Fatalf("settings.style.size 该被删掉, style = %v", style)
	}
	if style["color"] != "red" {
		t.Fatalf("settings.style.color = %v, want red（未提到的键不许动）", style["color"])
	}
	if settings["mode"] != "auto" {
		t.Fatalf("settings.mode = %v, want auto", settings["mode"])
	}
}

// 删一个本来就不存在的键 = 无变化，不能凭空制造一次 version+1（每次 diff 都可能
// 带上早已清掉的字段，无脑当成变更会让快照版本号无限空转）。
func TestPatchNodeNullOnMissingKeyIsNoop(t *testing.T) {
	payload := json.RawMessage(`{"nodes": [{"id": "node-1", "label": "x"}], "connections": []}`)

	_, changed, _, err := PatchNode(payload, "node-1", json.RawMessage(`{"taskId": null}`))
	if err != nil {
		t.Fatalf("PatchNode returned error: %v", err)
	}
	if changed {
		t.Fatal("删不存在的键不该算变更")
	}
}

func TestPatchNodeIgnoresPatchID(t *testing.T) {
	payload := json.RawMessage(`{"nodes": [{"id": "node-1", "label": "old"}], "connections": []}`)

	got, changed, _, err := PatchNode(payload, "node-1", json.RawMessage(`{"id": "node-2", "label": "new"}`))
	if err != nil {
		t.Fatalf("PatchNode returned error: %v", err)
	}
	if !changed {
		t.Fatal("PatchNode changed = false, want true")
	}

	node := firstNode(t, decodeObject(t, got))
	if node["id"] != "node-1" {
		t.Fatalf("node id = %v, want node-1", node["id"])
	}
	if node["label"] != "new" {
		t.Fatalf("node label = %v, want new", node["label"])
	}
}

func TestUpsertNodeCreatesSnapshotWhenPayloadEmpty(t *testing.T) {
	got, changed, _, err := UpsertNode(nil, json.RawMessage(`{"id": "node-1", "type": "text"}`))
	if err != nil {
		t.Fatalf("UpsertNode returned error: %v", err)
	}
	if !changed {
		t.Fatal("UpsertNode changed = false, want true")
	}

	doc := decodeObject(t, got)
	nodes := doc["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(nodes))
	}
	connections := doc["connections"].([]any)
	if len(connections) != 0 {
		t.Fatalf("connections len = %d, want 0", len(connections))
	}
	node := nodes[0].(map[string]any)
	if node["id"] != "node-1" {
		t.Fatalf("node id = %v, want node-1", node["id"])
	}
}

func TestUpsertNodeReplacesExistingNodeWithSameID(t *testing.T) {
	payload := json.RawMessage(`{
		"nodes": [
			{"id": "node-1", "type": "text", "label": "old"},
			{"id": "node-2", "type": "image"}
		],
		"connections": []
	}`)
	replacement := json.RawMessage(`{"id": "node-1", "type": "video", "label": "new"}`)

	got, changed, _, err := UpsertNode(payload, replacement)
	if err != nil {
		t.Fatalf("UpsertNode returned error: %v", err)
	}
	if !changed {
		t.Fatal("UpsertNode changed = false, want true")
	}

	doc := decodeObject(t, got)
	nodes := doc["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("nodes len = %d, want 2", len(nodes))
	}
	node := nodes[0].(map[string]any)
	if node["id"] != "node-1" {
		t.Fatalf("node id = %v, want node-1", node["id"])
	}
	if node["type"] != "video" {
		t.Fatalf("node type = %v, want video", node["type"])
	}
	if node["label"] != "new" {
		t.Fatalf("node label = %v, want new", node["label"])
	}
}

func TestUpsertNodeUnchangedReturnsChangedFalse(t *testing.T) {
	payload := json.RawMessage(`{"nodes": [{"id": "node-1", "type": "text"}], "connections": []}`)

	got, changed, _, err := UpsertNode(payload, json.RawMessage(`{"id": "node-1", "type": "text"}`))
	if err != nil {
		t.Fatalf("UpsertNode returned error: %v", err)
	}
	if changed {
		t.Fatal("UpsertNode changed = true, want false")
	}

	doc := decodeObject(t, got)
	nodes := doc["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(nodes))
	}
}

func TestDeleteNodeAlsoDeletesAttachedConnections(t *testing.T) {
	payload := json.RawMessage(`{
		"nodes": [
			{"id": "a"},
			{"id": "b"},
			{"id": "c"}
		],
		"connections": [
			{"id": "ab", "from": "a", "to": "b"},
			{"id": "bc", "from": "b", "to": "c"},
			{"id": "ca", "from": "c", "to": "a"}
		]
	}`)

	got, changed, _, err := DeleteNodes(payload, []string{"b"})
	if err != nil {
		t.Fatalf("DeleteNodes returned error: %v", err)
	}
	if !changed {
		t.Fatal("DeleteNodes changed = false, want true")
	}

	doc := decodeObject(t, got)
	nodes := doc["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("nodes len = %d, want 2", len(nodes))
	}
	connections := doc["connections"].([]any)
	if len(connections) != 1 {
		t.Fatalf("connections len = %d, want 1", len(connections))
	}
	connection := connections[0].(map[string]any)
	if connection["id"] != "ca" {
		t.Fatalf("remaining connection id = %v, want ca", connection["id"])
	}
}

func TestPatchNodePositionsUpdatesOnlyGeometry(t *testing.T) {
	payload := json.RawMessage(`{
		"nodes": [
			{"id": "node-1", "type": "text", "x": 1, "y": 2, "width": 100, "height": 80, "data": {"title": "keep"}},
			{"id": "node-2", "type": "image", "x": 10, "y": 20, "width": 30, "height": 40}
		],
		"connections": []
	}`)
	x := 50.0
	height := 120.0

	got, changed, _, err := PatchNodePositions(payload, []NodeGeometryPatch{{ID: "node-1", X: &x, Height: &height}})
	if err != nil {
		t.Fatalf("PatchNodePositions returned error: %v", err)
	}
	if !changed {
		t.Fatal("PatchNodePositions changed = false, want true")
	}

	doc := decodeObject(t, got)
	nodes := doc["nodes"].([]any)
	node := nodes[0].(map[string]any)
	if node["x"] != float64(50) {
		t.Fatalf("node x = %v, want 50", node["x"])
	}
	if node["y"] != float64(2) {
		t.Fatalf("node y = %v, want 2", node["y"])
	}
	if node["width"] != float64(100) {
		t.Fatalf("node width = %v, want 100", node["width"])
	}
	if node["height"] != float64(120) {
		t.Fatalf("node height = %v, want 120", node["height"])
	}
	data := node["data"].(map[string]any)
	if data["title"] != "keep" {
		t.Fatalf("node data.title = %v, want keep", data["title"])
	}
}

func TestPatchNodePositionsRejectsNaNAndInf(t *testing.T) {
	payload := json.RawMessage(`{"nodes": [{"id": "node-1"}], "connections": []}`)

	for name, value := range map[string]float64{
		"nan": math.NaN(),
		"inf": math.Inf(1),
	} {
		t.Run(name, func(t *testing.T) {
			_, changed, _, err := PatchNodePositions(payload, []NodeGeometryPatch{{ID: "node-1", X: &value}})
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("PatchNodePositions err = %v, want ErrInvalidPayload", err)
			}
			if changed {
				t.Fatal("PatchNodePositions changed = true, want false")
			}
		})
	}
}

func TestUpsertConnectionReplacesExistingConnectionWithSameID(t *testing.T) {
	payload := json.RawMessage(`{
		"nodes": [{"id": "a"}, {"id": "b"}],
		"connections": [
			{"id": "conn-1", "from": "a", "to": "b", "label": "old"},
			{"id": "conn-2", "from": "b", "to": "a"}
		]
	}`)
	replacement := json.RawMessage(`{"id": "conn-1", "from": "b", "to": "a", "label": "new"}`)

	got, changed, _, err := UpsertConnection(payload, replacement)
	if err != nil {
		t.Fatalf("UpsertConnection returned error: %v", err)
	}
	if !changed {
		t.Fatal("UpsertConnection changed = false, want true")
	}

	doc := decodeObject(t, got)
	connections := doc["connections"].([]any)
	if len(connections) != 2 {
		t.Fatalf("connections len = %d, want 2", len(connections))
	}
	connection := connections[0].(map[string]any)
	if connection["id"] != "conn-1" {
		t.Fatalf("connection id = %v, want conn-1", connection["id"])
	}
	if connection["from"] != "b" {
		t.Fatalf("connection from = %v, want b", connection["from"])
	}
	if connection["to"] != "a" {
		t.Fatalf("connection to = %v, want a", connection["to"])
	}
	if connection["label"] != "new" {
		t.Fatalf("connection label = %v, want new", connection["label"])
	}
}

func TestUpsertConnectionCreatesValidConnection(t *testing.T) {
	payload := json.RawMessage(`{
		"nodes": [{"id": "a"}, {"id": "b"}],
		"connections": []
	}`)
	connection := json.RawMessage(`{"id": "conn-1", "from": "a", "to": "b"}`)

	got, changed, _, err := UpsertConnection(payload, connection)
	if err != nil {
		t.Fatalf("UpsertConnection returned error: %v", err)
	}
	if !changed {
		t.Fatal("UpsertConnection changed = false, want true")
	}
	doc := decodeObject(t, got)
	connections := doc["connections"].([]any)
	if len(connections) != 1 {
		t.Fatalf("connections len = %d, want 1", len(connections))
	}
	stored := connections[0].(map[string]any)
	if stored["from"] != "a" || stored["to"] != "b" {
		t.Fatalf("stored endpoints = %v -> %v, want a -> b", stored["from"], stored["to"])
	}
}

func TestUpsertConnectionRejectsMissingEndpoints(t *testing.T) {
	payload := json.RawMessage(`{
		"nodes": [{"id": "a"}, {"id": "b"}],
		"connections": []
	}`)

	for name, connection := range map[string]json.RawMessage{
		"missing from": json.RawMessage(`{"id": "conn-1", "to": "b"}`),
		"empty from":   json.RawMessage(`{"id": "conn-1", "from": "", "to": "b"}`),
		"missing to":   json.RawMessage(`{"id": "conn-1", "from": "a"}`),
		"empty to":     json.RawMessage(`{"id": "conn-1", "from": "a", "to": ""}`),
	} {
		t.Run(name, func(t *testing.T) {
			_, changed, _, err := UpsertConnection(payload, connection)
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("UpsertConnection err = %v, want ErrInvalidPayload", err)
			}
			if changed {
				t.Fatal("UpsertConnection changed = true, want false")
			}
		})
	}
}

func TestUpsertConnectionRejectsMissingEndpointNode(t *testing.T) {
	payload := json.RawMessage(`{
		"nodes": [{"id": "a"}, {"id": "b"}],
		"connections": []
	}`)

	for name, connection := range map[string]json.RawMessage{
		"missing from node": json.RawMessage(`{"id": "conn-1", "from": "missing", "to": "b"}`),
		"missing to node":   json.RawMessage(`{"id": "conn-1", "from": "a", "to": "missing"}`),
	} {
		t.Run(name, func(t *testing.T) {
			_, changed, _, err := UpsertConnection(payload, connection)
			if !errors.Is(err, ErrNodeNotFound) {
				t.Fatalf("UpsertConnection err = %v, want ErrNodeNotFound", err)
			}
			if changed {
				t.Fatal("UpsertConnection changed = true, want false")
			}
		})
	}
}

func TestUpsertAndDeleteConnection(t *testing.T) {
	payload := json.RawMessage(`{
		"view": {"zoom": 1},
		"nodes": [{"id": "a"}, {"id": "b"}],
		"connections": []
	}`)
	connection := json.RawMessage(`{"id": "conn-1", "from": "a", "to": "b", "label": "first"}`)

	withConnection, changed, _, err := UpsertConnection(payload, connection)
	if err != nil {
		t.Fatalf("UpsertConnection returned error: %v", err)
	}
	if !changed {
		t.Fatal("UpsertConnection changed = false, want true")
	}
	doc := decodeObject(t, withConnection)
	if len(doc["connections"].([]any)) != 1 {
		t.Fatalf("connections len after upsert = %d, want 1", len(doc["connections"].([]any)))
	}
	if _, ok := doc["view"]; !ok {
		t.Fatal("top-level view was not preserved")
	}

	withoutConnection, changed, _, err := DeleteConnection(withConnection, "conn-1")
	if err != nil {
		t.Fatalf("DeleteConnection returned error: %v", err)
	}
	if !changed {
		t.Fatal("DeleteConnection changed = false, want true")
	}
	doc = decodeObject(t, withoutConnection)
	if len(doc["connections"].([]any)) != 0 {
		t.Fatalf("connections len after delete = %d, want 0", len(doc["connections"].([]any)))
	}

	_, changed, _, err = DeleteConnection(withoutConnection, "conn-1")
	if !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("DeleteConnection err = %v, want ErrConnectionNotFound", err)
	}
	if changed {
		t.Fatal("DeleteConnection changed = true for missing connection, want false")
	}
}

func decodeObject(t *testing.T, payload json.RawMessage) map[string]any {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v\npayload: %s", err, string(payload))
	}
	return doc
}

func firstNode(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()

	nodes, ok := doc["nodes"].([]any)
	if !ok {
		t.Fatalf("nodes has type %T, want []any", doc["nodes"])
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(nodes))
	}
	node, ok := nodes[0].(map[string]any)
	if !ok {
		t.Fatalf("node has type %T, want map[string]any", nodes[0])
	}
	return node
}
