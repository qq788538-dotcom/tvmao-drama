package canvasmanifest

import (
	"encoding/json"
	"testing"
)

// 清单要覆盖画布上**全部**节点类型，不只是 agent 能创建的那 8 种——read_canvas
// 得读得懂画布上任何一个节点。这个数字跟前端 store.ts 的 DEFAULT_SIZES 对齐。
func TestCoversAllNodeTypes(t *testing.T) {
	want := []string{
		"input-image", "input-audio", "text-node", "gen-image", "gen-video",
		"gen-audio", "video-input", "image-compare", "local-save", "preview",
		"subtitle-erase", "video-upscale", "video-whitemodel", "script", "director-desk",
	}
	for _, nt := range want {
		if !KnownNodeType(nt) {
			t.Errorf("清单缺节点类型 %q", nt)
		}
	}
	if got := len(Get().NodeTypes); got != len(want) {
		t.Errorf("节点类型数 = %d, want %d（多出来的类型说明前端加了新节点没同步清单）", got, len(want))
	}
}

// machine 字段连读都不给：回传内部运行时状态只会让 planner 把它当可操作对象。
func TestMachineFieldsAreInvisible(t *testing.T) {
	hidden := map[string][]string{
		"gen-video":     {"history", "historyIndex", "pendingGen"},
		"script":        {"gen"},
		"director-desk": {"directorProject"},
		"preview":       {"kind"},
		"video-input":   {"frames", "selectedKeyframes", "extractingFrames", "videoExpanded"},
	}
	for nodeType, fields := range hidden {
		visible := map[string]bool{}
		for _, f := range VisibleFields(nodeType) {
			visible[f] = true
		}
		for _, f := range fields {
			if visible[f] {
				t.Errorf("%s.%s 是 machine 字段，不该出现在 VisibleFields", nodeType, f)
			}
		}
	}
	// 三个通用机器字段对每种类型都不可见。
	for _, nodeType := range []string{"gen-image", "text-node", "script"} {
		for _, f := range VisibleFields(nodeType) {
			if f == "status" || f == "agentMeta" || f == "genHistory" {
				t.Errorf("%s.%s 是通用 machine 字段，不该可见", nodeType, f)
			}
		}
	}
}

// readonly 读得到但写不进。script.assets 是这一档存在的理由：rows 是 open 的，
// 而 row 的 prompt 用 @[类型-名称] 引用资产——不给读就写不出合法的分镜提示词。
func TestReadonlyVisibleButNotWritable(t *testing.T) {
	cases := []struct{ nodeType, field string }{
		{"script", "assets"},
		{"script", "globalStyle"},
		{"script", "durationSec"},
		{"gen-video", "scriptRowId"},
		{"subtitle-erase", "upstreamExpire"},
		{"video-input", "videoMeta"},
		{"preview", "sourceGenVideoId"},
	}
	for _, c := range cases {
		spec, ok := Field(c.nodeType, c.field)
		if !ok {
			t.Fatalf("清单里没有 %s.%s", c.nodeType, c.field)
		}
		if spec.Access != AccessReadonly {
			t.Errorf("%s.%s access = %q, want readonly", c.nodeType, c.field, spec.Access)
		}
		if !contains(VisibleFields(c.nodeType), c.field) {
			t.Errorf("%s.%s 是 readonly，应该可见", c.nodeType, c.field)
		}
		if contains(WritableFields(c.nodeType), c.field) {
			t.Errorf("%s.%s 是 readonly，不该可写", c.nodeType, c.field)
		}
	}
}

// 本次需求的直接目标：三个 gen 节点的 modelId 必须可写，且都标了「改它要重置
// 动态字段」——只写 modelId 会把旧模型的分辨率档位留在新模型上。
func TestModelIdWritableAndResetsDynamics(t *testing.T) {
	for _, nodeType := range []string{"gen-image", "gen-video", "gen-audio"} {
		spec, ok := Field(nodeType, "modelId")
		if !ok {
			t.Fatalf("%s 缺 modelId", nodeType)
		}
		if spec.Access != AccessOpen {
			t.Errorf("%s.modelId access = %q, want open", nodeType, spec.Access)
		}
		if !spec.ResetsDynamicFields {
			t.Errorf("%s.modelId 必须标 resetsDynamicFields", nodeType)
		}
		if !HasDynamicFields(nodeType) {
			t.Errorf("%s 应该有 modelUiSchema 动态字段", nodeType)
		}
	}
}

// 提示词字段名两个 gen 节点不一样，存储字段名以浏览器为准——CLI 曾在这栽过跟头。
func TestPromptFieldNamesMatchBrowser(t *testing.T) {
	if spec, ok := Field("gen-video", "videoPrompt"); !ok || spec.Access != AccessOpen {
		t.Error("gen-video 的提示词字段必须是 videoPrompt 且可写")
	}
	if _, ok := Field("gen-video", "prompt"); ok {
		t.Error("gen-video 不该有 prompt 字段——它存 videoPrompt，两边各读各的会让提示词全空")
	}
	if spec, ok := Field("gen-image", "prompt"); !ok || spec.Access != AccessOpen {
		t.Error("gen-image 的提示词字段必须是 prompt 且可写")
	}
}

// rows 的两条语义声明缺一不可：整体替换（数组合并没有意义）+ id 执行侧接管
// （锚断了会让批量铺节点的防重复计数失效，用户重复扣费）。
func TestScriptRowsSemantics(t *testing.T) {
	spec, ok := Field("script", "rows")
	if !ok {
		t.Fatal("script 缺 rows")
	}
	if spec.Access != AccessOpen {
		t.Errorf("rows access = %q, want open", spec.Access)
	}
	if !spec.WholeReplace {
		t.Error("rows 必须标 wholeReplace")
	}
	if !spec.IDManaged {
		t.Error("rows 必须标 idManaged")
	}
}

// 预设只存 key，完整提示词后端拼接不下发——所以开放给 agent 写不泄露任何东西。
func TestPresetKeysWritable(t *testing.T) {
	for _, f := range []string{"promptPreset", "presetParams"} {
		spec, ok := Field("gen-image", f)
		if !ok || spec.Access != AccessOpen {
			t.Errorf("gen-image.%s 应该可写", f)
		}
	}
}

// 每个字段都要有非空 desc——它直接进 agent 的工具输出，空描述等于让 agent 继续猜。
func TestEveryFieldDocumented(t *testing.T) {
	m := Get()
	check := func(scope string, fields map[string]FieldSpec) {
		for name, f := range fields {
			if f.Desc == "" {
				t.Errorf("%s.%s 缺 desc", scope, name)
			}
			if f.Type == "" {
				t.Errorf("%s.%s 缺 type", scope, name)
			}
			switch f.Access {
			case AccessOpen, AccessReadonly, AccessMachine:
			default:
				t.Errorf("%s.%s access = %q 非法", scope, name, f.Access)
			}
		}
	}
	check("common", m.CommonFields)
	for nt, spec := range m.NodeTypes {
		check(nt, spec.Fields)
	}
}

// 清单原文必须是合法 JSON 且能往返——TS 侧要 import 同一份文件。
func TestRawIsValidJSON(t *testing.T) {
	var probe map[string]any
	if err := json.Unmarshal(Raw(), &probe); err != nil {
		t.Fatalf("manifest.json 不是合法 JSON: %v", err)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
