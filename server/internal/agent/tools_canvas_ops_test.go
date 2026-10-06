// apps/api/internal/agent/tools_canvas_ops_test.go
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type stubCanvasReader struct{ payload json.RawMessage }

func (s *stubCanvasReader) GetLatestSnapshot(int64) (json.RawMessage, error) { return s.payload, nil }

func TestReadCanvasTool_CompactSnapshot(t *testing.T) {
	long := strings.Repeat("很", 500)
	payload := `{"nodes":[
		{"id":"n1","type":"gen-video","x":10,"y":20,"width":300,"height":200,"content":"","status":"idle","settings":{"name":"镜头1","videoPrompt":"` + long + `"}},
		{"id":"n2","type":"input-image","x":0,"y":0,"content":"media://k.png","settings":{"name":"角色A"}}
	],"connections":[{"from":"n2","to":"n1","inputType":"image"}]}`
	tool := &ReadCanvasTool{Reader: &stubCanvasReader{payload: json.RawMessage(payload)}}
	out, err := tool.InvokableRun(WithProjectID(context.Background(), 7), "{}")
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	var got struct {
		Nodes []readCanvasNode `json:"nodes"`
		Connections []readCanvasConn `json:"connections"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	if len(got.Nodes) != 2 || len(got.Connections) != 1 {
		t.Fatalf("shape mismatch: %+v", got)
	}
	// gen-video 的提示词从 videoPrompt 取，且截断到 200 字。
	if got.Nodes[0].Prompt == "" || len([]rune(got.Nodes[0].Prompt)) > 201 {
		t.Fatalf("videoPrompt not surfaced/truncated: %d runes", len([]rune(got.Nodes[0].Prompt)))
	}
	if !got.Nodes[1].HasMedia {
		t.Fatal("input-image with content should report has_media")
	}
}

func TestUpdateNodeTool_StripsMachineFields(t *testing.T) {
	var got []Artifact
	ctx := WithEmit(context.Background(), func(a Artifact) { got = append(got, a) })
	tool := &UpdateNodeTool{}
	out, err := tool.InvokableRun(ctx, `{"node_id":"n1","settings":"{\"videoPrompt\":\"新词\",\"status\":\"generating\",\"agentMeta\":{}}"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "已提交") || !strings.Contains(out, "系统管理") {
		t.Fatalf("result should note stripped fields, got %q", out)
	}
	if len(got) != 1 || got[0].ArtifactType != "node_update_info" {
		t.Fatalf("artifact mismatch: %+v", got)
	}
	var p struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	_ = json.Unmarshal(got[0].Payload, &p)
	if _, has := p.Settings["status"]; has {
		t.Fatal("status 是机器管理字段，agent 直写会伪造「生成中」假 UI，必须剥掉")
	}
	if _, has := p.Settings["videoPrompt"]; !has {
		t.Fatal("legit field was wrongly stripped")
	}
}

func TestCreateNodeTool_RejectsUnknownType(t *testing.T) {
	tool := &CreateNodeTool{}
	if _, err := tool.InvokableRun(context.Background(), `{"type":"magic-node"}`); err == nil || !strings.Contains(err.Error(), "available") {
		t.Fatalf("unknown type must be rejected with available list, got %v", err)
	}
}

func TestConnectNodesTool_RejectsSelfLoop(t *testing.T) {
	tool := &ConnectNodesTool{}
	if _, err := tool.InvokableRun(context.Background(), `{"from_node_id":"n1","to_node_id":"n1"}`); err == nil {
		t.Fatal("self loop must be rejected")
	}
}

func TestCanvasOpTools_EmitStructuredPayloads(t *testing.T) {
	var got []Artifact
	ctx := WithEmit(context.Background(), func(a Artifact) { got = append(got, a) })
	if _, err := (&CreateNodeTool{}).InvokableRun(ctx, `{"type":"gen-image","settings":"{\"prompt\":\"雪地柴犬\"}","x":100,"y":60}`); err != nil {
		t.Fatal(err)
	}
	if _, err := (&DeleteNodeTool{}).InvokableRun(ctx, `{"node_id":"n9"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := (&ConnectNodesTool{}).InvokableRun(ctx, `{"from_node_id":"a","to_node_id":"b"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := (&RemoveConnectionTool{}).InvokableRun(ctx, `{"from_node_id":"a","to_node_id":"b"}`); err != nil {
		t.Fatal(err)
	}
	wantTypes := []string{"node_create_info", "node_delete_info", "connection_create_info", "connection_delete_info"}
	if len(got) != len(wantTypes) {
		t.Fatalf("expected %d artifacts, got %d", len(wantTypes), len(got))
	}
	for i, w := range wantTypes {
		if got[i].ArtifactType != w || len(got[i].Payload) == 0 {
			t.Fatalf("artifact %d = %+v, want type %s with payload", i, got[i], w)
		}
	}
}

// 用户拍板规则：操作 chip 有名称显示名称，没名称才显示 id。删除类 chip 渲染
// 时节点已不在画布上，名字必须在执行时从服务端快照定格进 payload 落库——
// 这里钉死 delete / connect / remove 三个工具都带定格名（update 同 helper）。
func TestCanvasOps_FreezeNodeNameIntoPayload(t *testing.T) {
	snap := `{"nodes":[
		{"id":"n-a","type":"text-node","settings":{"name":"第一章剧本"}},
		{"id":"n-b","type":"gen-image","settings":{}}
	]}`
	reader := &stubSnapshotReader{raw: snap}
	var got []Artifact
	ctx := WithEmit(WithProjectID(context.Background(), 1), func(a Artifact) { got = append(got, a) })

	if _, err := (&DeleteNodeTool{Reader: reader}).InvokableRun(ctx, `{"node_id":"n-a"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := (&ConnectNodesTool{Reader: reader}).InvokableRun(ctx, `{"from_node_id":"n-a","to_node_id":"n-b"}`); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 artifacts, got %d", len(got))
	}

	var del struct {
		NodeName string `json:"node_name"`
	}
	if err := json.Unmarshal(got[0].Payload, &del); err != nil {
		t.Fatal(err)
	}
	if del.NodeName != "第一章剧本" {
		t.Fatalf("delete payload node_name = %q, want 第一章剧本", del.NodeName)
	}

	var conn struct {
		FromName string `json:"from_name"`
		ToName   string `json:"to_name"`
	}
	if err := json.Unmarshal(got[1].Payload, &conn); err != nil {
		t.Fatal(err)
	}
	// n-b 没起名 → 定格名为空串，前端回退 id——空串就是「没名字」的表达。
	if conn.FromName != "第一章剧本" || conn.ToName != "" {
		t.Fatalf("connect payload names = %q/%q", conn.FromName, conn.ToName)
	}
}
