package cliapi

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSemanticTypeMapping(t *testing.T) {
	cases := []struct {
		internal string
		semantic string
		writable bool
	}{
		{"input-image", "image-input", true},
		{"input-audio", "audio-input", true},
		{"video-input", "video-input", true},
		{"gen-image", "image-generator", true},
		{"gen-video", "video-generator", true},
		{"preview", "preview", false},
		{"text-node", "text-node", false},
		{"subtitle-erase", "subtitle-erase", false},
		{"video-upscale", "video-upscale", false},
		{"video-whitemodel", "video-whitemodel", false},
		// 类型清单还在增长（subtitle-erase 刚上测试环境）。未知类型必须原样透传、
		// 标只读，不能报错也不能丢弃——否则每加一个节点类型就要重发 CLI 二进制。
		{"未来才会有的类型", "未来才会有的类型", false},
		{"", "", false},
	}
	for _, c := range cases {
		sem, w := SemanticType(c.internal)
		if sem != c.semantic || w != c.writable {
			t.Errorf("SemanticType(%q) = (%q,%v), want (%q,%v)", c.internal, sem, w, c.semantic, c.writable)
		}
	}
}

func TestInternalTypeOnlyAcceptsWritableFour(t *testing.T) {
	for sem, want := range map[string]string{
		"image-input":     "input-image",
		"audio-input":     "input-audio",
		"video-input":     "video-input",
		"image-generator": "gen-image",
		"video-generator": "gen-video",
	} {
		got, ok := InternalType(sem)
		if !ok || got != want {
			t.Errorf("InternalType(%s) = (%s,%v), want (%s,true)", sem, got, ok, want)
		}
	}
	// 内部类型名不能当语义类型用——否则 CLI 契约会同时接受两套命名。
	for _, sem := range []string{"preview", "text-node", "gen-image", "input-image", "nope", ""} {
		if _, ok := InternalType(sem); ok {
			t.Errorf("InternalType(%q) 不该可写", sem)
		}
	}
}

func signStub(s string) string { return s + "?sig=1" }

func TestNormalizeHistoryThreeShapes(t *testing.T) {
	// 老格式：纯 URL 字符串数组（生产 1079 个节点）
	got := NormalizeHistory([]any{"http://a/1.png", "", "http://a/2.png"}, nil)
	if len(got) != 2 || got[0].URL != "http://a/1.png" || got[1].URL != "http://a/2.png" {
		t.Fatalf("string history = %+v", got)
	}

	// 新格式：对象数组，prompt/params/at 必须保留
	got = NormalizeHistory([]any{
		map[string]any{
			"url":    "http://a/3.png",
			"prompt": "雨",
			"params": map[string]any{"resolution": "2K"},
			"at":     float64(1700),
		},
	}, nil)
	if len(got) != 1 || got[0].Prompt != "雨" || got[0].Params["resolution"] != "2K" || got[0].At != 1700 {
		t.Fatalf("object history = %+v", got)
	}

	// 空数组 + 有 content：按「只有一条、用 content 初始化」，
	// 与 canvasgen/writeback.go 的 normalizeHistory 逐字同构。
	got = NormalizeHistory([]any{}, "http://a/4.png")
	if len(got) != 1 || got[0].URL != "http://a/4.png" {
		t.Fatalf("fallback history = %+v", got)
	}

	// 空数组 + 无 content（生产/测试都有：未生成过的节点）
	if got := NormalizeHistory(nil, nil); len(got) != 0 {
		t.Fatalf("empty history = %+v", got)
	}
	// 无 url 的对象条目要被丢弃，与前后端同构逻辑一致
	if got := NormalizeHistory([]any{map[string]any{"prompt": "只有词没有图"}}, nil); len(got) != 0 {
		t.Fatalf("无 url 条目应丢弃, got %+v", got)
	}
}

func TestToNodeStripsInternalKeysAndSignsURLs(t *testing.T) {
	raw := map[string]any{
		"id": "n-abc", "type": "gen-image",
		"x": 10.0, "y": 20.0, "width": 280.0, "height": 280.0,
		"status": "succeeded", "content": "http://a/out.png", "taskId": float64(9),
		"settings": map[string]any{
			"prompt": "雨后城市", "modelId": "doubao-image", "ratio": "16:9",
			"kind": "image", "assetId": float64(5),
			"history": []any{"http://a/out.png"}, "historyIndex": float64(0),
			"pendingGen": map[string]any{"prompt": "x"}, "error": "旧错误",
		},
	}
	n := ToNode(raw, signStub)

	if n.Type != "image-generator" || !n.Writable || n.InternalType != "gen-image" {
		t.Fatalf("type mapping wrong: %+v", n)
	}
	if n.Position.X != 10 || n.Position.Y != 20 || n.Size.Width != 280 || n.Size.Height != 280 {
		t.Fatalf("geometry = %+v %+v", n.Position, n.Size)
	}
	if n.Content != "http://a/out.png?sig=1" {
		t.Fatalf("content 未签名: %s", n.Content)
	}
	if n.Params["prompt"] != "雨后城市" || n.Params["modelId"] != "doubao-image" || n.Params["ratio"] != "16:9" {
		t.Fatalf("params 丢失: %+v", n.Params)
	}
	// 内部键必须留在服务端。泄漏出去等于把内部结构变成公共 API。
	for _, k := range []string{"history", "historyIndex", "pendingGen", "error", "kind", "assetId"} {
		if _, ok := n.Params[k]; ok {
			t.Fatalf("内部键 %s 泄漏进 params", k)
		}
	}
	if n.Media == nil || n.Media.Kind != "image" || n.Media.AssetID != 5 {
		t.Fatalf("media = %+v", n.Media)
	}
	if n.Error != "旧错误" || n.TaskID != 9 {
		t.Fatalf("error/taskId 丢失: %+v", n)
	}
	if len(n.History) != 1 || n.History[0].URL != "http://a/out.png?sig=1" {
		t.Fatalf("history 未签名或丢失: %+v", n.History)
	}
}

func TestToNodeUnknownTypeIsReadonlyNotError(t *testing.T) {
	n := ToNode(map[string]any{"id": "n-x", "type": "brand-new-thing"}, nil)
	if n.Type != "brand-new-thing" || n.Writable {
		t.Fatalf("未知类型应原样透传且只读: %+v", n)
	}
	if n.Params == nil {
		t.Fatal("Params 必须是非 nil map —— JSON 里要出 {} 而不是 null")
	}
	if n.History == nil {
		t.Fatal("History 必须是非 nil slice —— JSON 里要出 [] 而不是 null")
	}
}

func TestToNodeWithoutSignerKeepsRawURLs(t *testing.T) {
	n := ToNode(map[string]any{"id": "n-x", "type": "preview", "content": "http://a/1.png"}, nil)
	if n.Content != "http://a/1.png" {
		t.Fatalf("content = %s", n.Content)
	}
}

func TestParseCanvas(t *testing.T) {
	payload := json.RawMessage(`{
		"nodes": [
			{"id":"n-1","type":"input-image","x":0,"y":0,"width":260,"height":260,"content":"http://a/1.png"},
			{"id":"n-2","type":"gen-image","x":100,"y":0,"width":280,"height":280,"settings":{"modelId":"m1"}},
			{"id":"n-3","type":"preview","x":200,"y":0,"width":320,"height":260,"settings":{"kind":"video"}}
		],
		"connections": [
			{"id":"c-1","from":"n-1","to":"n-2"},
			{"id":"c-2","from":"n-3","to":"n-2","inputType":"video"}
		],
		"view": {"x":0,"y":0,"zoom":1}
	}`)

	c, err := ParseCanvas(payload, 17, signStub)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 17 || len(c.Nodes) != 3 || len(c.Edges) != 2 {
		t.Fatalf("canvas = %+v", c)
	}
	// 连线顺序即输入顺序契约，不得重排。
	if c.Edges[0].ID != "c-1" || c.Edges[1].ID != "c-2" {
		t.Fatalf("edge 顺序被改: %+v", c.Edges)
	}
	if c.Edges[0].FromNodeID != "n-1" || c.Edges[0].ToNodeID != "n-2" {
		t.Fatalf("edge 端点错: %+v", c.Edges[0])
	}
	if c.Edges[1].InputType != "video" {
		t.Fatalf("inputType 丢失: %+v", c.Edges[1])
	}
	if c.Nodes[0].Content != "http://a/1.png?sig=1" {
		t.Fatalf("node content 未签名: %s", c.Nodes[0].Content)
	}
}

func TestParseCanvasEmptyPayload(t *testing.T) {
	// 新项目还没存过快照，payload 是空的——必须回空画布而不是报错。
	for _, raw := range []json.RawMessage{nil, json.RawMessage(""), json.RawMessage("{}"), json.RawMessage("null")} {
		c, err := ParseCanvas(raw, 0, nil)
		if err != nil {
			t.Fatalf("payload %q: %v", raw, err)
		}
		if c.Nodes == nil || c.Edges == nil {
			t.Fatalf("payload %q: nodes/edges 必须是非 nil 空数组, got %+v", raw, c)
		}
		if len(c.Nodes) != 0 || len(c.Edges) != 0 {
			t.Fatalf("payload %q: %+v", raw, c)
		}
	}
}

func TestParseCanvasBrokenPayloadErrors(t *testing.T) {
	if _, err := ParseCanvas(json.RawMessage(`{not json`), 1, nil); err == nil {
		t.Fatal("损坏的快照必须报错，不能静默当空画布——那会让用户以为自己的画布没了")
	}
}

func TestWritableSemanticTypesIsStable(t *testing.T) {
	// 这五个是公共契约的一部分；增删要同步 CLI 与文档。
	want := []string{"image-input", "audio-input", "video-input", "image-generator", "video-generator"}
	if got := WritableSemanticTypes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("WritableSemanticTypes() = %v, want %v", got, want)
	}
}
