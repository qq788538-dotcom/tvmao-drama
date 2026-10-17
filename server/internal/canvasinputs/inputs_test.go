package canvasinputs

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func node(id, ty, content string, settings map[string]any) map[string]any {
	n := map[string]any{"id": id, "type": ty}
	if content != "" {
		n["content"] = content
	}
	if settings != nil {
		n["settings"] = settings
	}
	return n
}

func conn(id, from, to string) map[string]any {
	return map[string]any{"id": id, "from": from, "to": to}
}

// assertJSONEqual 比 JSON 序列化后的结果，规避 int / float64 的比较噪音。
func assertJSONEqual(t *testing.T, want, got map[string]any) {
	t.Helper()
	w, _ := json.Marshal(want)
	g, _ := json.Marshal(got)
	if string(w) != string(g) {
		t.Fatalf("inputs 不一致:\n want %s\n got  %s", w, g)
	}
}

// ---- ① 上游分类与顺序（对齐 store.ts 的 getConnectedInputs） ----

// 生产入度最高的真实结构：n-DK1PBmF9（gen-video，7 条入边），
// 按数组顺序是 4 个 input-image → 2 个 input-audio → 1 个 input-image。
// 这些顺序完全由数组位置承载，没有任何显式标记。
func TestConnectedPreservesArrayOrder(t *testing.T) {
	snap := Snapshot{
		Nodes: []map[string]any{
			node("i1", "input-image", "http://x/1.png", nil),
			node("i2", "input-image", "http://x/2.png", nil),
			node("i3", "input-image", "http://x/3.png", nil),
			node("i4", "input-image", "http://x/4.png", nil),
			node("a1", "input-audio", "http://x/1.mp3", nil),
			node("a2", "input-audio", "http://x/2.mp3", nil),
			node("i5", "input-image", "http://x/5.png", nil),
			node("v", "gen-video", "", nil),
		},
		Connections: []map[string]any{
			conn("c1", "i1", "v"), conn("c2", "i2", "v"), conn("c3", "i3", "v"), conn("c4", "i4", "v"),
			conn("c5", "a1", "v"), conn("c6", "a2", "v"), conn("c7", "i5", "v"),
		},
	}
	got := Connected(snap, "v", nil)

	wantImgs := []string{"http://x/1.png", "http://x/2.png", "http://x/3.png", "http://x/4.png", "http://x/5.png"}
	if !reflect.DeepEqual(got.Images, wantImgs) {
		t.Fatalf("images = %v, want %v（数组位置即输入顺序）", got.Images, wantImgs)
	}
	if !reflect.DeepEqual(got.Audios, []string{"http://x/1.mp3", "http://x/2.mp3"}) {
		t.Fatalf("audios = %v", got.Audios)
	}
}

// 建边顺序颠倒，images 顺序必须跟着颠倒——否则「第一张参考图」会错位。
func TestConnectedOrderFollowsConnectionsNotNodes(t *testing.T) {
	nodes := []map[string]any{
		node("a", "input-image", "http://x/a.png", nil),
		node("b", "input-image", "http://x/b.png", nil),
		node("g", "gen-image", "", nil),
	}
	forward := Connected(Snapshot{Nodes: nodes, Connections: []map[string]any{
		conn("c1", "a", "g"), conn("c2", "b", "g"),
	}}, "g", nil)
	reverse := Connected(Snapshot{Nodes: nodes, Connections: []map[string]any{
		conn("c2", "b", "g"), conn("c1", "a", "g"),
	}}, "g", nil)

	if forward.Images[0] != "http://x/a.png" || reverse.Images[0] != "http://x/b.png" {
		t.Fatalf("顺序未跟随 connections: %v / %v", forward.Images, reverse.Images)
	}
}

// preview 是通用媒体容器，必须读 settings.kind，不能靠节点 type。
func TestConnectedClassifiesPreviewBySettingsKind(t *testing.T) {
	snap := Snapshot{
		Nodes: []map[string]any{
			node("p1", "preview", "http://x/gen/task-1-0", map[string]any{"kind": "video"}),
			node("p2", "preview", "http://x/gen/task-2-0", map[string]any{"kind": "image"}),
			node("g", "gen-video", "", nil),
		},
		Connections: []map[string]any{conn("c1", "p1", "g"), conn("c2", "p2", "g")},
	}
	got := Connected(snap, "g", nil)
	if len(got.Videos) != 1 || got.Videos[0] != "http://x/gen/task-1-0" {
		t.Fatalf("videos = %v", got.Videos)
	}
	if len(got.Images) != 1 || got.Images[0] != "http://x/gen/task-2-0" {
		t.Fatalf("images = %v", got.Images)
	}
}

// 去字幕/转高清/转白模产物的签名 URL 没有扩展名，只有节点 type 靠得住。
// 漏这个白名单在线上出过：去字幕产物被当成图片喂给转高清。
func TestConnectedVideoOutputTypeWhitelist(t *testing.T) {
	for _, ty := range []string{"gen-video", "video-input", "subtitle-erase", "video-upscale", "video-whitemodel"} {
		snap := Snapshot{
			Nodes:       []map[string]any{node("s", ty, "http://x/gen/task-9-0", nil), node("d", "gen-video", "", nil)},
			Connections: []map[string]any{conn("c", "s", "d")},
		}
		if got := Connected(snap, "d", nil); len(got.Videos) != 1 {
			t.Errorf("%s 应归为视频, got %+v", ty, got)
		}
	}
}

func TestConnectedAudioExtensionFallback(t *testing.T) {
	for _, ext := range []string{"mp3", "wav", "aac", "ogg", "flac", "m4a", "M4A"} {
		snap := Snapshot{
			Nodes:       []map[string]any{node("p", "preview", "http://x/a."+ext, nil), node("d", "gen-video", "", nil)},
			Connections: []map[string]any{conn("c", "p", "d")},
		}
		if got := Connected(snap, "d", nil); len(got.Audios) != 1 {
			t.Errorf(".%s 应归为音频: %+v", ext, got)
		}
	}
}

// 音频判断必须在视频白名单之前：input-audio 节点即便 URL 像视频也是音频。
func TestConnectedAudioTypeBeatsExtension(t *testing.T) {
	snap := Snapshot{
		Nodes:       []map[string]any{node("a", "input-audio", "http://x/weird.mp4", nil), node("g", "gen-video", "", nil)},
		Connections: []map[string]any{conn("c", "a", "g")},
	}
	got := Connected(snap, "g", nil)
	if len(got.Audios) != 1 || len(got.Videos) != 0 {
		t.Fatalf("input-audio 必须归音频: %+v", got)
	}
}

// gen-audio 产物 / declaredKind=audio 必须归音频——产物签名 URL 无扩展名
// （gen/task-N-0），漏了类型分支会掉进 images 兜底（与前端 store.ts 同构）。
func TestConnectedGenAudioAndDeclaredKind(t *testing.T) {
	snap := Snapshot{
		Nodes: []map[string]any{
			node("ga", "gen-audio", "http://x/gen/task-7-0", nil),
			node("pv", "preview", "http://x/gen/task-8-0", map[string]any{"kind": "audio"}),
			node("g", "gen-video", "", nil),
		},
		Connections: []map[string]any{conn("c1", "ga", "g"), conn("c2", "pv", "g")},
	}
	got := Connected(snap, "g", nil)
	if len(got.Audios) != 2 || len(got.Images) != 0 || len(got.Videos) != 0 {
		t.Fatalf("gen-audio/declaredKind=audio 必须归音频: %+v", got)
	}
	if got.AudioIDs[0] != "ga" || got.AudioIDs[1] != "pv" {
		t.Fatalf("AudioIDs 顺序应随 connections: %+v", got.AudioIDs)
	}
}

func TestConnectedTextNodesGoToTexts(t *testing.T) {
	snap := Snapshot{
		Nodes: []map[string]any{
			node("t1", "text-node", "", map[string]any{"text": "第一段"}),
			node("t2", "text-node", "", map[string]any{"text": ""}), // 空文本跳过
			node("t3", "text-node", "", nil),                        // 无 settings 跳过
			node("t4", "text-node", "", map[string]any{"text": "第二段"}),
			node("g", "gen-image", "", nil),
		},
		Connections: []map[string]any{
			conn("c1", "t1", "g"), conn("c2", "t2", "g"), conn("c3", "t3", "g"), conn("c4", "t4", "g"),
		},
	}
	got := Connected(snap, "g", nil)
	if !reflect.DeepEqual(got.Texts, []string{"第一段", "第二段"}) {
		t.Fatalf("texts = %v", got.Texts)
	}
	if len(got.Images) != 0 {
		t.Fatalf("text-node 不该进 images: %+v", got)
	}
}

// 注意:media:// 在这里被跳过只因为 sign=nil(恒等)。生产的 sign 是
// MediaSigner.Sign,会把 media:// 解析成签名 https 后收进来——见
// media_resolve_test.go 的回归用例;这里守的是"解析不了的引用不进 inputs"。
func TestConnectedSkipsNonHTTPContent(t *testing.T) {
	snap := Snapshot{
		Nodes: []map[string]any{
			node("a", "input-image", "", nil),
			node("b", "input-image", "data:image/png;base64,AAA", nil),
			node("c", "input-image", "media://obj/1.png", nil),
			node("d", "input-image", "https://x/ok.png", nil),
			node("g", "gen-image", "", nil),
		},
		Connections: []map[string]any{conn("c1", "a", "g"), conn("c2", "b", "g"), conn("c3", "c", "g"), conn("c4", "d", "g")},
	}
	got := Connected(snap, "g", nil)
	if !reflect.DeepEqual(got.Images, []string{"https://x/ok.png"}) {
		t.Fatalf("images = %v", got.Images)
	}
}

func TestConnectedIgnoresOtherNodesEdges(t *testing.T) {
	snap := Snapshot{
		Nodes: []map[string]any{
			node("a", "input-image", "http://x/a.png", nil),
			node("g1", "gen-image", "", nil),
			node("g2", "gen-image", "", nil),
		},
		Connections: []map[string]any{conn("c1", "a", "g1")},
	}
	if got := Connected(snap, "g2", nil); len(got.Images) != 0 {
		t.Fatalf("不该拿到别人的入边: %+v", got)
	}
}

func TestConnectedSignsURLs(t *testing.T) {
	snap := Snapshot{
		Nodes:       []map[string]any{node("a", "input-image", "http://x/a.png", nil), node("g", "gen-image", "", nil)},
		Connections: []map[string]any{conn("c", "a", "g")},
	}
	got := Connected(snap, "g", func(s string) string { return s + "?sig=1" })
	if got.Images[0] != "http://x/a.png?sig=1" {
		t.Fatalf("未签名: %v", got.Images)
	}
}

// ---- ② uiSchema 默认值合并（golden 来自前端 generationInputs.test.ts） ----

var goldenSchema = json.RawMessage(`{
	"ratio":      {"label":"比例","default":"16:9","options":[]},
	"resolution": {"label":"分辨率","default":"2K","options":[]},
	"n":          {"label":"数量","default":2,"options":[]},
	"duration":   {"label":"时长","default":5,"options":[]}
}`)

func TestBuildDynamicInputsUsesSettingsFirstThenSchemaDefault(t *testing.T) {
	got := BuildDynamicInputs(goldenSchema, map[string]any{"ratio": "1:1", "n": float64(4)})
	want := map[string]any{"ratio": "1:1", "resolution": "2K", "n": float64(4), "duration": float64(5)}
	assertJSONEqual(t, want, got)
}

// 前端是 `field.default ?? ”` 的两级兜底：连 default 都没有时是空串而不是缺键。
// 缺键会让上游按自己的默认值跑，那和网页提交就不一样了。
func TestBuildDynamicInputsMissingDefaultBecomesEmptyString(t *testing.T) {
	got := BuildDynamicInputs(json.RawMessage(`{"style":{"label":"风格","options":[]}}`), nil)
	if got["style"] != "" {
		t.Fatalf(`want "", got %#v`, got["style"])
	}
}

// settings 里有但不在 schema 里的键不进 dynamic —— prompt/modelId 走别的通道。
func TestBuildDynamicInputsIgnoresNonSchemaSettings(t *testing.T) {
	got := BuildDynamicInputs(goldenSchema, map[string]any{"prompt": "词", "modelId": "m1"})
	if _, ok := got["prompt"]; ok {
		t.Fatalf("prompt 不该进 dynamic: %+v", got)
	}
	if _, ok := got["modelId"]; ok {
		t.Fatalf("modelId 不该进 dynamic: %+v", got)
	}
}

func TestBuildDynamicInputsEmptySchema(t *testing.T) {
	if got := BuildDynamicInputs(nil, map[string]any{"ratio": "1:1"}); len(got) != 0 {
		t.Fatalf("空 schema 应产出空 map, got %+v", got)
	}
}

// ---- ③ 最终 inputs（golden 逐字对齐前端 generationInputs.test.ts） ----

func TestBuildImageInputsGolden(t *testing.T) {
	got := BuildImageInputs("", "上游文本",
		map[string]any{"ratio": "1:1", "resolution": "1K", "n": float64(2)},
		[]string{"https://example.com/a.png", "https://example.com/b.png"})
	want := map[string]any{
		"prompt": "上游文本", "ratio": "1:1", "resolution": "1K", "n": float64(2),
		"size": "1024x1024", "width": 1024, "height": 1024,
		"images": []string{"https://example.com/a.png", "https://example.com/b.png"},
		"image":  "https://example.com/a.png",
	}
	assertJSONEqual(t, want, got)
}

func TestBuildVideoInputsGolden(t *testing.T) {
	got := BuildVideoInputs("镜头推进", "备用文本",
		map[string]any{"ratio": "16:9", "resolution": "720P", "duration": float64(8)},
		[]string{"https://example.com/a.png"},
		[]string{"https://example.com/a.mp4"},
		[]string{"https://example.com/a.mp3"})
	want := map[string]any{
		// 上游文本节点的词在前、节点自身的词在后——与前端 golden 逐字一致。
		"prompt": "备用文本\n镜头推进", "ratio": "16:9", "resolution": "720P", "duration": float64(8),
		"size": "1280x720", "width": 1280, "height": 720,
		"images": []string{"https://example.com/a.png"},
		"videos": []string{"https://example.com/a.mp4"},
		"audios": []string{"https://example.com/a.mp3"},
	}
	assertJSONEqual(t, want, got)
}

func TestBuildImageInputsNoPromptPlaceholder(t *testing.T) {
	got := BuildImageInputs("   ", "", map[string]any{}, nil)
	if got["prompt"] != "(no prompt)" {
		t.Fatalf("prompt = %v", got["prompt"])
	}
	// dynamic 为空时 ratio/resolution 走 buildImageGenerationInputs 内的 1:1 / 1K 兜底。
	if got["size"] != "1024x1024" {
		t.Fatalf("size = %v", got["size"])
	}
	// 空图列表时前端的 image 是 undefined → JSON 里该键不存在。
	// 写成 "" 会让上游收到一张空图 URL。
	if _, ok := got["image"]; ok {
		t.Fatalf("空图列表不应出现 image 键: %+v", got)
	}
}

// golden：与前端 generationInputs.test.ts 的 `describe('composePrompt')` 同一张表。
// 两个提示词来源是互补的——上游文本节点的词在前、节点自身的词在后，都提交。
func TestComposePrompt(t *testing.T) {
	cases := []struct{ name, prompt, fallback, want string }{
		{"两边都有 → 上游在前、节点在后，\\n 分隔", "本地词", "上游词", "上游词\n本地词"},
		{"只有节点自身 → 原样", "本地词", "", "本地词"},
		{"只有上游文本 → 原样", "  ", "上游词", "上游词"},
		{"两边都空 → 占位符", "  ", "   ", "(no prompt)"},
	}
	for _, c := range cases {
		if got := BuildImageInputs(c.prompt, c.fallback, nil, nil)["prompt"]; got != c.want {
			t.Errorf("%s: composePrompt(%q,%q) = %v, want %q", c.name, c.prompt, c.fallback, got, c.want)
		}
	}
}

// 前端是 {prompt, ...dynamic, size, width, height, ...}：派生值在展开之后，
// 恒覆盖 dynamic 里的同名键。调用方在 settings 里塞一个 size 不该污染结果。
func TestDerivedKeysOverrideDynamicInputs(t *testing.T) {
	got := BuildImageInputs("x", "",
		map[string]any{"ratio": "1:1", "resolution": "1K", "size": "污染", "width": 1, "height": 2}, nil)
	if got["size"] != "1024x1024" || got["width"] != 1024 || got["height"] != 1024 {
		t.Fatalf("派生值必须覆盖 dynamic: %+v", got)
	}
}

// 前端传的是 []，JSON 出来是 []；Go 的 nil slice 会序列化成 null，上游可能不认。
func TestBuildInputsEmptyMediaAreEmptyArraysNotNull(t *testing.T) {
	blob, _ := json.Marshal(BuildVideoInputs("x", "", map[string]any{}, nil, nil, nil))
	for _, k := range []string{`"images":[]`, `"videos":[]`, `"audios":[]`} {
		if !strings.Contains(string(blob), k) {
			t.Fatalf("缺 %s: %s", k, blob)
		}
	}
	blob, _ = json.Marshal(BuildImageInputs("x", "", map[string]any{}, nil))
	if !strings.Contains(string(blob), `"images":[]`) {
		t.Fatalf("缺 images:[]: %s", blob)
	}
}

func TestBuildVideoInputsDefaultRatioIs169(t *testing.T) {
	// 图片默认 1:1 / 1K，视频默认 16:9 / 720P —— 两者不同，混用会算出错误尺寸。
	img := BuildImageInputs("x", "", map[string]any{}, nil)
	vid := BuildVideoInputs("x", "", map[string]any{}, nil, nil, nil)
	if img["size"] != "1024x1024" || vid["size"] != "1280x720" {
		t.Fatalf("默认值串了: image=%v video=%v", img["size"], vid["size"])
	}
}

func TestParseSnapshot(t *testing.T) {
	snap, err := ParseSnapshot(json.RawMessage(`{"nodes":[{"id":"a"}],"connections":[{"id":"c"}]}`))
	if err != nil || len(snap.Nodes) != 1 || len(snap.Connections) != 1 {
		t.Fatalf("snap = %+v err = %v", snap, err)
	}
	if _, err := ParseSnapshot(nil); err != nil {
		t.Fatalf("空 payload 不该报错: %v", err)
	}
	if _, err := ParseSnapshot(json.RawMessage(`{broken`)); err == nil {
		t.Fatal("损坏 payload 必须报错")
	}
}
