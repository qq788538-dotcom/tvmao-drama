package canvasgen

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
)

func nodeOf(t *testing.T, payload json.RawMessage, id string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	for _, raw := range doc["nodes"].([]any) {
		if n := raw.(map[string]any); n["id"] == id {
			return n
		}
	}
	t.Fatalf("node %s not found", id)
	return nil
}

func TestOnGenerationResultMarksUnsaved(t *testing.T) {
	fake := &fakeMutator{payload: json.RawMessage(`{"nodes":[{"id":"n1","type":"gen-image","settings":{}}]}`)}
	s := &Service{snaps: fake, now: time.Now}
	ref := json.RawMessage(`{"kind":"canvas_node","projectId":1,"nodeId":"n1","userId":1}`)
	err := s.OnGenerationResult(context.Background(), ref, generation.SinkResult{
		TaskID: 42, OutputURLs: []string{"https://cdn.example.com/a.png?x=1&y=2"}, AssetIDs: []int64{0},
	})
	if err != nil {
		t.Fatal(err)
	}
	st := nodeOf(t, fake.result, "n1")["settings"].(map[string]any)
	if st["unsaved"] != true || st["genTaskId"] != float64(42) {
		t.Fatalf("未落本地应标 unsaved + genTaskId, got %v", st)
	}
}

func TestOnGenerationResultLocalOutputNotUnsaved(t *testing.T) {
	fake := &fakeMutator{payload: json.RawMessage(`{"nodes":[{"id":"n1","type":"gen-image","settings":{"unsaved":true,"genTaskId":1}}]}`)}
	s := &Service{snaps: fake, now: time.Now}
	ref := json.RawMessage(`{"kind":"canvas_node","projectId":1,"nodeId":"n1"}`)
	if err := s.OnGenerationResult(context.Background(), ref, generation.SinkResult{
		TaskID: 43, OutputURLs: []string{"media://u1/gen/task-43-0"}, AssetIDs: []int64{5},
	}); err != nil {
		t.Fatal(err)
	}
	st := nodeOf(t, fake.result, "n1")["settings"].(map[string]any)
	if _, ok := st["unsaved"]; ok {
		t.Fatalf("落了本地的结果不应带 unsaved, got %v", st)
	}
	if _, ok := st["genTaskId"]; ok {
		t.Fatalf("genTaskId 应一并清掉, got %v", st)
	}
}

// URL 带 & 时 json.Marshal 会写成 \u0026，按字节替换（RewriteMediaRef）改不到；
// ApplyRepersist 按节点结构改，必须改对。
func TestApplyRepersistRewritesNodeAndClearsFlag(t *testing.T) {
	old := "https://cdn.example.com/a.png?x=1&y=2"
	snap, _ := json.Marshal(map[string]any{"nodes": []any{map[string]any{
		"id": "n1", "type": "gen-image", "content": old,
		"settings": map[string]any{"unsaved": true, "genTaskId": 42, "history": []any{"media://older", map[string]any{"url": old}}},
	}}})
	if !strings.Contains(string(snap), `\u0026`) {
		t.Fatal("前提：快照里的 \u0026 被转义")
	}
	fake := &fakeMutator{payload: snap}
	notifier := &fakeNotifier{}
	s := &Service{snaps: fake, notifier: notifier, now: time.Now}
	err := s.ApplyRepersist(context.Background(),
		json.RawMessage(`{"kind":"canvas_node","projectId":3,"nodeId":"n1","userId":1}`),
		[]generation.RepersistReplacement{{Index: 0, OldURL: old, NewURL: "media://u1/gen/task-42-0", AssetID: 77}})
	if err != nil {
		t.Fatal(err)
	}
	n := nodeOf(t, fake.result, "n1")
	st := n["settings"].(map[string]any)
	if n["content"] != "media://u1/gen/task-42-0" {
		t.Fatalf("content 应换成本地地址, got %v", n["content"])
	}
	if hist := st["history"].([]any); hist[1].(map[string]any)["url"] != "media://u1/gen/task-42-0" || hist[0] != "media://older" {
		t.Fatalf("history 里对应条目应改写、其他条目原样, got %v", hist)
	}
	if _, ok := st["unsaved"]; ok {
		t.Fatalf("unsaved 应清掉, got %v", st)
	}
	if st["assetId"] != float64(77) {
		t.Fatalf("第 0 个槽位应写回 assetId, got %v", st["assetId"])
	}
	if len(notifier.calls) != 1 || notifier.calls[0].projectID != 3 {
		t.Fatalf("回写后应广播一次 canvas_changed, got %+v", notifier.calls)
	}
}

func TestApplyRepersistIgnoresNonCanvasSink(t *testing.T) {
	s := &Service{snaps: &fakeMutator{}, now: time.Now}
	if err := s.ApplyRepersist(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyRepersist(context.Background(), json.RawMessage(`{"kind":"other"}`), nil); err != nil {
		t.Fatal(err)
	}
}
