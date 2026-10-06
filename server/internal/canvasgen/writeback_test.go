package canvasgen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/canvasstate"
)

type fakeMutator struct {
	payload json.RawMessage
	result  json.RawMessage
	changed bool
	version int64
}

func (f *fakeMutator) MutateSnapshot(_ int64, fn func(json.RawMessage) (json.RawMessage, bool, error)) (int64, error) {
	next, changed, err := fn(f.payload)
	f.result, f.changed = next, changed
	if f.version == 0 {
		f.version = 9
	}
	return f.version, err
}

// notifyRecord 记一次广播调用，用来钉住「写成功才发帧、no-op 不发帧」。
type notifyRecord struct {
	userID    int64
	projectID int64
	version   int64
	mark      string
	op        *canvasstate.CanvasOpSummary
}

type fakeNotifier struct{ calls []notifyRecord }

func (f *fakeNotifier) NotifyCanvasChanged(userID, projectID, version int64, sourceMark string, op *canvasstate.CanvasOpSummary) {
	f.calls = append(f.calls, notifyRecord{userID, projectID, version, sourceMark, op})
}

// 回写只动目标节点;其它节点、目标节点的未知字段、连接等一律原样保留。
func TestWriteNode_OnlyTouchesTargetNodePreservingRest(t *testing.T) {
	snap := `{"nodes":[
		{"id":"A","type":"gen-image","status":"generating","taskId":7,"settings":{"error":"旧错误","history":["x"]},"customField":42},
		{"id":"B","type":"gen-image","content":"other","status":"succeeded"}
	],"connections":[{"id":"c1","from":"A","to":"B"}]}`
	fake := &fakeMutator{payload: json.RawMessage(snap)}
	s := &Service{snaps: fake}

	s.writeNode(0, 1, "A", func(node map[string]any) {
		node["content"] = "https://img/A.png"
		node["status"] = "succeeded"
		delete(node, "taskId")
		clearNodeError(node)
	})

	if !fake.changed {
		t.Fatal("expected changed=true")
	}
	var doc struct {
		Nodes []map[string]any `json:"nodes"`
		Conns []map[string]any `json:"connections"`
	}
	if err := json.Unmarshal(fake.result, &doc); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	byID := map[string]map[string]any{}
	for _, n := range doc.Nodes {
		byID[n["id"].(string)] = n
	}

	a := byID["A"]
	if a["content"] != "https://img/A.png" || a["status"] != "succeeded" {
		t.Fatalf("target node not updated: %+v", a)
	}
	if _, ok := a["taskId"]; ok {
		t.Fatalf("taskId should be cleared: %+v", a)
	}
	if a["customField"].(float64) != 42 {
		t.Fatalf("unknown field customField dropped: %+v", a)
	}
	settings := a["settings"].(map[string]any)
	if _, ok := settings["error"]; ok {
		t.Fatalf("settings.error should be cleared: %+v", settings)
	}
	if hist, ok := settings["history"].([]any); !ok || len(hist) != 1 {
		t.Fatalf("settings.history should be preserved: %+v", settings)
	}

	b := byID["B"]
	if b["content"] != "other" || b["status"] != "succeeded" {
		t.Fatalf("sibling node B was modified: %+v", b)
	}
	if len(doc.Conns) != 1 {
		t.Fatalf("connections dropped: %+v", doc.Conns)
	}
}

// 节点已被用户删除 → 无处可写,不改动、changed=false。
func TestWriteNode_MissingNodeIsNoop(t *testing.T) {
	fake := &fakeMutator{payload: json.RawMessage(`{"nodes":[{"id":"A"}]}`)}
	s := &Service{snaps: fake}
	s.writeNode(0, 1, "ghost", func(node map[string]any) { node["content"] = "x" })
	if fake.changed {
		t.Fatal("expected no change when target node missing")
	}
}

// 后端回写成功 → 广播一帧 canvas_changed(sourceMark 恒 "server"、version 取写入后的版本);
// 节点已删的 no-op 不广播——否则每个失效任务都会让全体观看端白拉一次快照。
func TestWriteNode_NotifiesOnlyOnRealChange(t *testing.T) {
	fake := &fakeMutator{payload: json.RawMessage(`{"nodes":[{"id":"A"}]}`), version: 12}
	notifier := &fakeNotifier{}
	s := &Service{snaps: fake, notifier: notifier}

	s.writeNode(77, 5, "A", func(node map[string]any) { node["content"] = "x" })
	if len(notifier.calls) != 1 {
		t.Fatalf("写成功应广播 1 次, got %d", len(notifier.calls))
	}
	got := notifier.calls[0]
	if got.userID != 77 || got.projectID != 5 || got.version != 12 || got.mark != "server" {
		t.Fatalf("广播参数 %+v, want user=77 project=5 version=12 mark=server", got)
	}

	s.writeNode(77, 5, "ghost", func(node map[string]any) { node["content"] = "x" })
	if len(notifier.calls) != 1 {
		t.Fatalf("no-op 不该广播, got %d 次", len(notifier.calls))
	}
}

// 回写广播必须带 op 摘要（kind=upsertNode + 回写后的整节点）：没有它，正在编辑的
// 同伴收到帧只能整拉快照，出图那一刻就把人家的选中/拖拽/文本光标全清一遍。
// 用整节点而不是 patch，是因为回写会 delete 字段（taskId），patch 语义表达不了删除。
func TestWriteNode_NotifyCarriesUpsertNodeOp(t *testing.T) {
	fake := &fakeMutator{payload: json.RawMessage(`{"nodes":[{"id":"A","taskId":7,"status":"generating"}]}`), version: 3}
	notifier := &fakeNotifier{}
	s := &Service{snaps: fake, notifier: notifier}

	s.writeNode(77, 5, "A", func(node map[string]any) {
		node["content"] = "https://img/A.png"
		node["status"] = "succeeded"
		delete(node, "taskId")
	})

	op := notifier.calls[0].op
	if op == nil || op.Kind != canvasstate.OpUpsertNode {
		t.Fatalf("op = %+v, want kind=upsertNode", op)
	}
	var node map[string]any
	if err := json.Unmarshal(op.Node, &node); err != nil {
		t.Fatalf("op.node 不是合法 JSON: %v", err)
	}
	if node["content"] != "https://img/A.png" || node["status"] != "succeeded" {
		t.Fatalf("op.node = %v, 必须是回写后的整节点", node)
	}
	if _, stale := node["taskId"]; stale {
		t.Fatalf("op.node 里还留着已删除的 taskId: %v", node)
	}
}

// fakeSigner 模拟 http.MediaSigner：把 media://<key> 换成签名 URL。
type fakeSigner struct{}

func (fakeSigner) SignJSON(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.ReplaceAll(string(raw), "media://", "https://signed.example/"))
}

// 出图回写的 content 是 canonical media://，而前端把它直接当 <img src>——浏览器不认
// 这个协议。一期同伴走 GET 快照(那条路上有 SignJSON)，二期本地合并绕开了唯一签名出口，
// 不补签就是「A 出图 → B 画布上这张图整块裂开」且零报错。
// 落库的字节必须仍是 canonical（签名 URL 会过期，写进快照就是定时死链）。
func TestWriteNode_OpMediaIsSignedButSnapshotStaysCanonical(t *testing.T) {
	fake := &fakeMutator{payload: json.RawMessage(`{"nodes":[{"id":"A"}]}`), version: 3}
	notifier := &fakeNotifier{}
	s := &Service{snaps: fake, notifier: notifier, signer: fakeSigner{}}

	s.writeNode(77, 5, "A", func(node map[string]any) { node["content"] = "media://k1" })

	op := notifier.calls[0].op
	if op == nil || strings.Contains(string(op.Node), "media://") {
		t.Fatalf("op.node 没被签名: %+v", op)
	}
	if !strings.Contains(string(op.Node), "https://signed.example/k1") {
		t.Fatalf("op.node = %s", op.Node)
	}
	if !strings.Contains(string(fake.result), "media://k1") {
		t.Fatalf("落库快照被签名 URL 污染: %s", fake.result)
	}
}

// 未装配 signer（单测/未 wire）时回写照常，op 里保持 canonical——接收端最差退回整拉。
func TestWriteNode_NilSignerKeepsCanonicalOp(t *testing.T) {
	fake := &fakeMutator{payload: json.RawMessage(`{"nodes":[{"id":"A"}]}`)}
	notifier := &fakeNotifier{}
	s := &Service{snaps: fake, notifier: notifier}

	s.writeNode(77, 5, "A", func(node map[string]any) { node["content"] = "media://k1" })

	if op := notifier.calls[0].op; op == nil || !strings.Contains(string(op.Node), "media://k1") {
		t.Fatalf("无 signer 时 op = %+v", op)
	}
}

// 未装配 notifier(单测/未 wire)时回写照常，不 panic。
func TestWriteNode_NilNotifierIsSafe(t *testing.T) {
	fake := &fakeMutator{payload: json.RawMessage(`{"nodes":[{"id":"A"}]}`)}
	s := &Service{snaps: fake}
	if err := s.writeNode(77, 5, "A", func(node map[string]any) { node["content"] = "x" }); err != nil {
		t.Fatalf("writeNode err=%v", err)
	}
	if !fake.changed {
		t.Fatal("expected changed=true")
	}
}
