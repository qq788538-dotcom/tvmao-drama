package cliapi

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestNewIDsMatchFrontendShape(t *testing.T) {
	// 前端是 `n-${nanoid(8)}` / `e-${nanoid(8)}`，字母表 URL-safe。
	// 形状不一致不会立刻出错，但会让「这个节点是谁建的」在排查时变得可猜——
	// 保持一致就没有这种隐性分叉。
	nodeRe := regexp.MustCompile(`^n-[A-Za-z0-9_-]{8}$`)
	edgeRe := regexp.MustCompile(`^e-[A-Za-z0-9_-]{8}$`)

	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := NewNodeID()
		if !nodeRe.MatchString(id) {
			t.Fatalf("node id 形状不对: %q", id)
		}
		if seen[id] {
			t.Fatalf("node id 重复: %q", id)
		}
		seen[id] = true

		if e := NewEdgeID(); !edgeRe.MatchString(e) {
			t.Fatalf("edge id 形状不对: %q", e)
		}
	}
}

func TestBuildNodeJSONFillsDefaults(t *testing.T) {
	raw, err := BuildNodeJSON(CreateNodeRequest{
		Type:     "image-generator",
		Position: &Position{X: 100, Y: 200},
		Params:   map[string]any{"prompt": "雨后城市", "modelId": "doubao-image", "ratio": "16:9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var n map[string]any
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatal(err)
	}

	if n["type"] != "gen-image" {
		t.Fatalf("type = %v，必须落内部类型", n["type"])
	}
	if n["x"] != 100.0 || n["y"] != 200.0 {
		t.Fatalf("position = %v %v", n["x"], n["y"])
	}
	if n["width"] != 280.0 || n["height"] != 280.0 {
		t.Fatalf("默认尺寸 = %v x %v", n["width"], n["height"])
	}
	if n["status"] != "idle" {
		t.Fatalf("status = %v；未运行节点必须明确是 idle，不能让调用方靠有没有 URL 猜", n["status"])
	}
	s, _ := n["settings"].(map[string]any)
	if s["prompt"] != "雨后城市" || s["modelId"] != "doubao-image" || s["ratio"] != "16:9" {
		t.Fatalf("params 未落进 settings: %v", s)
	}
}

// 默认尺寸必须与前端 store.ts 的 DEFAULT_SIZES 一致，否则 CLI 建的节点
// 在网页上大小突兀。
func TestBuildNodeJSONDefaultSizesMatchFrontend(t *testing.T) {
	cases := map[string][2]float64{
		"image-input":     {260, 260},
		"audio-input":     {300, 200},
		"image-generator": {280, 280},
		"video-generator": {400, 500},
	}
	for sem, want := range cases {
		params := map[string]any{}
		if strings.HasSuffix(sem, "-generator") {
			params["modelId"] = "m1"
		}
		raw, err := BuildNodeJSON(CreateNodeRequest{Type: sem, Params: params})
		if err != nil {
			t.Fatalf("%s: %v", sem, err)
		}
		var n map[string]any
		_ = json.Unmarshal(raw, &n)
		if n["width"] != want[0] || n["height"] != want[1] {
			t.Errorf("%s 尺寸 = %vx%v, want %vx%v", sem, n["width"], n["height"], want[0], want[1])
		}
	}
}

func TestBuildNodeJSONRejectsReadonlyType(t *testing.T) {
	var ce *Error
	// 只读类型用**内部名**传进来时，报 NODE_READONLY_TYPE 而不是 INVALID_NODE_TYPE——
	// 用户显然是想操作这个类型，只是它不可写，错误码要能说清区别。
	for _, ty := range []string{"preview", "text-node", "subtitle-erase", "video-upscale", "video-whitemodel", "local-save"} {
		_, err := BuildNodeJSON(CreateNodeRequest{Type: ty})
		if !errors.As(err, &ce) || ce.Code != CodeNodeReadonlyType {
			t.Errorf("%s 应报 NODE_READONLY_TYPE, got %v", ty, err)
		}
	}
	for _, ty := range []string{"根本不存在", ""} {
		_, err := BuildNodeJSON(CreateNodeRequest{Type: ty})
		if !errors.As(err, &ce) || ce.Code != CodeInvalidNodeType {
			t.Errorf("%q 应报 INVALID_NODE_TYPE, got %v", ty, err)
		}
	}
}

// 生产 52% 的成功 gen-image 节点没存 modelId，根因是网页只在点模型选择器时写。
// CLI 比网页更严格：不指定就不让建，绝不再制造新的无 modelId 节点。
func TestBuildNodeJSONGeneratorRequiresModel(t *testing.T) {
	var ce *Error
	for _, ty := range []string{"image-generator", "video-generator"} {
		_, err := BuildNodeJSON(CreateNodeRequest{Type: ty, Params: map[string]any{"prompt": "x"}})
		if !errors.As(err, &ce) || ce.Code != CodeModelRequired {
			t.Errorf("%s 缺 modelId 应报 MODEL_REQUIRED, got %v", ty, err)
		}
		_, err = BuildNodeJSON(CreateNodeRequest{Type: ty, Params: map[string]any{"modelId": "  "}})
		if !errors.As(err, &ce) || ce.Code != CodeModelRequired {
			t.Errorf("%s 空白 modelId 也应报 MODEL_REQUIRED, got %v", ty, err)
		}
	}
	// 输入节点不需要 modelId。
	for _, ty := range []string{"image-input", "audio-input"} {
		if _, err := BuildNodeJSON(CreateNodeRequest{Type: ty}); err != nil {
			t.Errorf("%s 不该要求 modelId: %v", ty, err)
		}
	}
}

func TestBuildNodeJSONRejectsInternalParamKeys(t *testing.T) {
	var ce *Error
	for _, k := range []string{"history", "historyIndex", "pendingGen", "kind", "assetId"} {
		_, err := BuildNodeJSON(CreateNodeRequest{
			Type:   "image-input",
			Params: map[string]any{k: "x"},
		})
		if !errors.As(err, &ce) || ce.Code != CodeInvalidArgument {
			t.Errorf("内部键 %s 不该被接受, got %v", k, err)
		}
	}
}

func TestBuildNodeJSONInputNodeCarriesContent(t *testing.T) {
	raw, err := BuildNodeJSON(CreateNodeRequest{
		Type:    "image-input",
		Content: "http://obj/a.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	var n map[string]any
	_ = json.Unmarshal(raw, &n)
	if n["content"] != "http://obj/a.png" {
		t.Fatalf("content = %v", n["content"])
	}
	// 有内容的输入节点是「已就绪」，不是 idle——下游取它当输入时要能用。
	if n["status"] != "succeeded" {
		t.Fatalf("带 content 的输入节点 status = %v, want succeeded", n["status"])
	}
}

func TestBuildNodeJSONExplicitSizeOverridesDefault(t *testing.T) {
	raw, _ := BuildNodeJSON(CreateNodeRequest{
		Type: "image-input",
		Size: &Size{Width: 500, Height: 400},
	})
	var n map[string]any
	_ = json.Unmarshal(raw, &n)
	if n["width"] != 500.0 || n["height"] != 400.0 {
		t.Fatalf("显式尺寸未生效: %vx%v", n["width"], n["height"])
	}
}

func TestBuildNodePatchIsMergeNotReplace(t *testing.T) {
	prompt := "新词"
	patch, err := BuildNodePatch("gen-image", PatchNodeRequest{
		Params:   map[string]any{"prompt": prompt},
		Position: &Position{X: 5, Y: 6},
	})
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	_ = json.Unmarshal(patch, &p)

	// patch 里**只能有请求里出现的字段**。多带一个 settings.ratio:null
	// 就会把用户在网页刚设的比例抹掉。
	if _, ok := p["width"]; ok {
		t.Fatalf("未请求的字段不得出现在 patch 里: %v", p)
	}
	if p["x"] != 5.0 || p["y"] != 6.0 {
		t.Fatalf("position patch = %v", p)
	}
	s, _ := p["settings"].(map[string]any)
	if len(s) != 1 || s["prompt"] != "新词" {
		t.Fatalf("settings patch 应只含 prompt: %v", s)
	}
}

func TestBuildNodePatchEmptyIsError(t *testing.T) {
	var ce *Error
	_, err := BuildNodePatch("gen-image", PatchNodeRequest{})
	if !errors.As(err, &ce) || ce.Code != CodeInvalidArgument {
		t.Fatalf("空 patch 应报错而不是发一个 no-op 请求, got %v", err)
	}
}

func TestBuildNodePatchRejectsInternalKeys(t *testing.T) {
	var ce *Error
	_, err := BuildNodePatch("gen-image", PatchNodeRequest{Params: map[string]any{"history": []any{}}})
	if !errors.As(err, &ce) || ce.Code != CodeInvalidArgument {
		t.Fatalf("got %v", err)
	}
}

// video-input 是第五个可写类型：上传视频后要能建输入节点。
// 曾经它在 knownReadonlyTypes 里，upload --create-node 到这一步会被拒。
func TestBuildNodeJSONVideoInput(t *testing.T) {
	raw, err := BuildNodeJSON(CreateNodeRequest{
		Type:    "video-input",
		Content: "media://k/v.mp4",
	})
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]any
	_ = json.Unmarshal(raw, &node)
	if node["type"] != "video-input" {
		t.Fatalf("type = %v", node["type"])
	}
	// 尺寸对齐前端 DEFAULT_SIZES['video-input']。
	if node["width"] != 580.0 || node["height"] != 460.0 {
		t.Fatalf("默认尺寸 = %vx%v, want 580x460（对齐前端）", node["width"], node["height"])
	}
	// 带内容的输入节点是「已就绪」，下游取它当输入时要能直接用。
	if node["status"] != "succeeded" {
		t.Fatalf("status = %v", node["status"])
	}
}
