package canvasgen

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/generation"
)

// 后端权威把结果并入 settings.history。这条路径的存在理由:恒异步后用户提交完就
// 可能关页面(生视频动辄几分钟),那时没有前端来追加这一条。
//
// 下面每个用例都对应一种线上真实存在的节点形态——尤其 noPendingGen 那个:蓝绿发版
// 切流那一刻,已经在跑的任务是**旧前端**提交的,快照里根本没有 pendingGen。

func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC) }
}

func sinkRef(t *testing.T, nodeID string) json.RawMessage {
	t.Helper()
	ref, err := json.Marshal(canvasSinkRef{Kind: sinkKindCanvasNode, ProjectID: 1, NodeID: nodeID})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// 跑一次成功回写,返回目标节点。
func runSuccess(t *testing.T, snap string, result generation.SinkResult) map[string]any {
	t.Helper()
	fake := &fakeMutator{payload: json.RawMessage(snap)}
	s := &Service{snaps: fake, now: fixedClock()}
	if err := s.OnGenerationResult(context.Background(), sinkRef(t, "A"), result); err != nil {
		t.Fatalf("OnGenerationResult: %v", err)
	}
	var doc struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal(fake.result, &doc); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	for _, n := range doc.Nodes {
		if n["id"] == "A" {
			return n
		}
	}
	t.Fatal("node A missing from result")
	return nil
}

func historyOf(t *testing.T, node map[string]any) []map[string]any {
	t.Helper()
	settings, ok := node["settings"].(map[string]any)
	if !ok {
		t.Fatalf("node has no settings: %+v", node)
	}
	raw, ok := settings["history"].([]any)
	if !ok {
		t.Fatalf("settings.history missing or wrong type: %+v", settings)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		item, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("history entry not an object: %+v", entry)
		}
		out = append(out, item)
	}
	return out
}

// 用户关着页面时出的第 2 张图:必须进 history 并带上提交时记下的提示词。
// 这正是改造前丢记录的那条路径。
func TestOnGenerationResult_AppendsHistoryWithPendingPrompt(t *testing.T) {
	snap := `{"nodes":[{"id":"A","type":"gen-image","status":"generating","taskId":7,
		"content":"media://v1",
		"settings":{"history":[{"url":"media://v1","prompt":"一只猫"}],"historyIndex":0,
		            "pendingGen":{"prompt":"一只橘猫,逆光","params":{"ratio":"16:9"}}}}]}`
	node := runSuccess(t, snap, generation.SinkResult{OutputURLs: []string{"media://v2"}, AssetIDs: []int64{99}})

	hist := historyOf(t, node)
	if len(hist) != 2 {
		t.Fatalf("expected 2 history entries, got %d: %+v", len(hist), hist)
	}
	if hist[1]["url"] != "media://v2" || hist[1]["prompt"] != "一只橘猫,逆光" {
		t.Fatalf("new entry wrong: %+v", hist[1])
	}
	if params, ok := hist[1]["params"].(map[string]any); !ok || params["ratio"] != "16:9" {
		t.Fatalf("params not carried: %+v", hist[1])
	}
	if hist[1]["at"] != "2026-07-27T10:00:00Z" {
		t.Fatalf("timestamp wrong: %+v", hist[1])
	}
	// 老条目原样保留,不被改写。
	if hist[0]["url"] != "media://v1" || hist[0]["prompt"] != "一只猫" {
		t.Fatalf("existing entry mutated: %+v", hist[0])
	}

	settings := node["settings"].(map[string]any)
	if settings["historyIndex"].(float64) != 1 {
		t.Fatalf("historyIndex should point at the new entry: %+v", settings)
	}
	if _, ok := settings["pendingGen"]; ok {
		t.Fatalf("pendingGen should be consumed: %+v", settings)
	}
	// assetId/kind 也得后端补——否则关着页面出的图没有 asset 身份,
	// 「保存到素材库」和合规校验都用不了。
	if settings["assetId"].(float64) != 99 || settings["kind"] != "image" {
		t.Fatalf("assetId/kind not written: %+v", settings)
	}
	if node["content"] != "media://v2" || node["status"] != "succeeded" {
		t.Fatalf("content/status not updated: %+v", node)
	}
}

// 蓝绿发版切流那一刻正在跑的任务:旧前端提交的,快照里没有 pendingGen。
// 必须照常进 history,只是不带提示词——绝不能因此报错或丢结果。
func TestOnGenerationResult_NoPendingGenStillAppends(t *testing.T) {
	snap := `{"nodes":[{"id":"A","type":"gen-video","status":"generating","taskId":7,
		"content":"media://v1","settings":{"history":["media://v1"],"historyIndex":0}}]}`
	node := runSuccess(t, snap, generation.SinkResult{OutputURLs: []string{"media://v2"}})

	hist := historyOf(t, node)
	if len(hist) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(hist), hist)
	}
	if hist[1]["url"] != "media://v2" {
		t.Fatalf("new entry wrong: %+v", hist[1])
	}
	if _, ok := hist[1]["prompt"]; ok {
		t.Fatalf("must not invent a prompt when none was recorded: %+v", hist[1])
	}
	// 老的纯字符串条目被归一化成对象,URL 不变。
	if hist[0]["url"] != "media://v1" {
		t.Fatalf("legacy string entry not normalized: %+v", hist[0])
	}
	if node["settings"].(map[string]any)["kind"] != "video" {
		t.Fatalf("kind should follow node type: %+v", node["settings"])
	}
}

// 老快照:有 content 没 history(更早期的节点)。兜底必须与前端 normalizeHistory
// 逐字同构——否则「关页面 vs 不关页面」会得到不同的条数。
func TestOnGenerationResult_LegacyContentOnlyNodeKeepsOldResult(t *testing.T) {
	snap := `{"nodes":[{"id":"A","type":"gen-image","status":"generating",
		"content":"media://old","settings":{}}]}`
	node := runSuccess(t, snap, generation.SinkResult{OutputURLs: []string{"media://new"}})

	hist := historyOf(t, node)
	if len(hist) != 2 || hist[0]["url"] != "media://old" || hist[1]["url"] != "media://new" {
		t.Fatalf("legacy content should become the first version: %+v", hist)
	}
}

// 全新节点的第一次生成:只有一条。
func TestOnGenerationResult_FirstGenerationCreatesSingleEntry(t *testing.T) {
	snap := `{"nodes":[{"id":"A","type":"gen-image","status":"generating","settings":{}}]}`
	node := runSuccess(t, snap, generation.SinkResult{OutputURLs: []string{"media://v1"}})

	hist := historyOf(t, node)
	if len(hist) != 1 || hist[0]["url"] != "media://v1" {
		t.Fatalf("expected exactly one entry: %+v", hist)
	}
}

// 幂等:页面开着时前端也会写同一条,sink 还可能重投。同 URL 只留一条,
// 重复调用只把 historyIndex 指过去。
func TestOnGenerationResult_SameURLIsIdempotent(t *testing.T) {
	snap := `{"nodes":[{"id":"A","type":"gen-image","status":"generating","content":"media://v1",
		"settings":{"history":[{"url":"media://v1"},{"url":"media://v2","prompt":"前端已写"}],
		            "historyIndex":0,"pendingGen":{"prompt":"重投不该覆盖它"}}}]}`
	node := runSuccess(t, snap, generation.SinkResult{OutputURLs: []string{"media://v2"}})

	hist := historyOf(t, node)
	if len(hist) != 2 {
		t.Fatalf("duplicate URL must not grow history: %+v", hist)
	}
	if hist[1]["prompt"] != "前端已写" {
		t.Fatalf("existing entry must not be overwritten: %+v", hist[1])
	}
	settings := node["settings"].(map[string]any)
	if settings["historyIndex"].(float64) != 1 {
		t.Fatalf("historyIndex should point at the existing entry: %+v", settings)
	}
}

// 失败回写不碰 history——失败没有结果可记,也不该动用户正在看的那一版。
func TestOnGenerationResult_FailureLeavesHistoryAlone(t *testing.T) {
	snap := `{"nodes":[{"id":"A","type":"gen-image","status":"generating","taskId":7,
		"content":"media://v1","settings":{"history":["media://v1"],"historyIndex":0}}]}`
	fake := &fakeMutator{payload: json.RawMessage(snap)}
	s := &Service{snaps: fake, now: fixedClock()}
	if err := s.OnGenerationResult(context.Background(), sinkRef(t, "A"), generation.SinkResult{Err: "上游炸了"}); err != nil {
		t.Fatalf("OnGenerationResult: %v", err)
	}

	var doc struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal(fake.result, &doc); err != nil {
		t.Fatal(err)
	}
	settings := doc.Nodes[0]["settings"].(map[string]any)
	hist := settings["history"].([]any)
	// 原样保留,连归一化都不做——失败路径完全不碰这个字段。
	if len(hist) != 1 || hist[0] != "media://v1" {
		t.Fatalf("history must be untouched on failure: %+v", hist)
	}
	if doc.Nodes[0]["content"] != "media://v1" {
		t.Fatalf("content must be kept on failure: %+v", doc.Nodes[0])
	}
}
