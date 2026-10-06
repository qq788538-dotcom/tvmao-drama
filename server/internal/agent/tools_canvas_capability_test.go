package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// fakeCatalog 是一个只有两个模型的模型目录：一个配了 uiSchema（有值域），
// 一个没配（自由输入）。
type fakeCatalog struct{}

func (fakeCatalog) ListCanvasModels() ([]CanvasModelInfo, error) {
	return []CanvasModelInfo{
		{ModelID: "minimax-h3", Name: "MiniMax H3", Type: "video", UISchema: json.RawMessage(`{
			"ratio": {"label":"比例","default":"16:9","options":[{"value":"16:9","label":"横屏"},{"value":"9:16","label":"竖屏"}]},
			"duration": {"label":"时长","default":5,"options":[{"value":5,"label":"5秒"},{"value":10,"label":"10秒"}]}
		}`)},
		{ModelID: "bare-model", Name: "Bare", Type: "video"},
	}, nil
}

func (f fakeCatalog) GetCanvasModel(modelID string) (*CanvasModelInfo, bool) {
	all, _ := f.ListCanvasModels()
	for i := range all {
		if all[i].ModelID == modelID {
			return &all[i], true
		}
	}
	return nil, false
}

func rawSettings(t *testing.T, jsonStr string) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
		t.Fatalf("bad test json: %v", err)
	}
	return out
}

// 线上实测的那次失败：planner 猜了个 `model` 字段名（真名 modelId），把模型
// 显示名当值写进去，工具回它「已提交修改」。现在必须报错，而且要告诉它能写什么。
func TestUnknownFieldRejectedWithHint(t *testing.T) {
	s := rawSettings(t, `{"model":"MiniMax H3"}`)
	v := validateNodeSettings(fakeCatalog{}, "gen-video", s, "minimax-h3")
	if len(v.Errors) == 0 {
		t.Fatal("写不存在的字段必须报错，静默成功正是本次要根治的问题")
	}
	msg := strings.Join(v.Errors, " ")
	if !strings.Contains(msg, "model") {
		t.Errorf("报错要点名是哪个字段: %s", msg)
	}
	if !strings.Contains(msg, "modelId") {
		t.Errorf("报错必须给出可写字段清单，否则 agent 只能换个姿势再猜: %s", msg)
	}
}

// 只读字段写入报错，且说明理由（不是「不存在」——agent 读得到它，说不存在会更迷惑）。
func TestReadonlyFieldRejected(t *testing.T) {
	s := rawSettings(t, `{"assets":{"characters":[]}}`)
	v := validateNodeSettings(fakeCatalog{}, "script", s, "")
	if len(v.Errors) == 0 {
		t.Fatal("写只读字段必须报错")
	}
	if !strings.Contains(v.Errors[0], "只读") {
		t.Errorf("报错要说明是只读而非不存在: %s", v.Errors[0])
	}
}

// 机器字段保持静默剥离（历史行为）：agent 压根看不见它们，写进来多半是幻觉，
// 为它报错只会让对话被无意义的纠错占满。
func TestMachineFieldsStrippedSilently(t *testing.T) {
	s := rawSettings(t, `{"status":"generating","history":[],"videoPrompt":"ok"}`)
	v := validateNodeSettings(fakeCatalog{}, "gen-video", s, "minimax-h3")
	if len(v.Errors) > 0 {
		t.Fatalf("机器字段不该报错，应静默剥离: %v", v.Errors)
	}
	if len(v.Stripped) != 2 {
		t.Errorf("应剥掉 status/history，got %v", v.Stripped)
	}
	if _, ok := s["status"]; ok {
		t.Error("status 应被就地删除")
	}
	if _, ok := s["videoPrompt"]; !ok {
		t.Error("合法字段不该被误删")
	}
}

// 动态字段（比例/时长）的值域跟着所选模型走。
func TestDynamicFieldValueChecked(t *testing.T) {
	ok := rawSettings(t, `{"ratio":"9:16","duration":10}`)
	if v := validateNodeSettings(fakeCatalog{}, "gen-video", ok, "minimax-h3"); len(v.Errors) > 0 {
		t.Fatalf("合法值不该报错: %v", v.Errors)
	}
	bad := rawSettings(t, `{"duration":7}`)
	v := validateNodeSettings(fakeCatalog{}, "gen-video", bad, "minimax-h3")
	if len(v.Errors) == 0 {
		t.Fatal("模型不支持的档位必须报错")
	}
	if !strings.Contains(v.Errors[0], "5") || !strings.Contains(v.Errors[0], "10") {
		t.Errorf("报错要列出允许值: %s", v.Errors[0])
	}
}

// 换模型时，动态字段按**新**模型判定——校验发生在写入之后的世界里。
func TestDynamicFieldsJudgedByNewModel(t *testing.T) {
	// bare-model 没配 uiSchema，此时 ratio 无从判定合法性，按未知字段拒绝：
	// 放行等于回到静默失败（写进去没人读）。
	s := rawSettings(t, `{"modelId":"bare-model","ratio":"16:9"}`)
	v := validateNodeSettings(fakeCatalog{}, "gen-video", s, "bare-model")
	if len(v.Errors) == 0 {
		t.Fatal("模型没配 uiSchema 时，动态字段无从判定，应拒绝而不是放行")
	}
}

// 未知节点类型不拦——节点类型清单还在增长，当封闭枚举会让每加一种类型就要重发后端。
func TestUnknownNodeTypeFallsBackToLegacy(t *testing.T) {
	s := rawSettings(t, `{"whatever":1,"status":"x"}`)
	v := validateNodeSettings(fakeCatalog{}, "some-future-node", s, "")
	if len(v.Errors) > 0 {
		t.Errorf("未知类型不该报错: %v", v.Errors)
	}
	if len(v.Stripped) != 1 {
		t.Errorf("未知类型仍要剥机器字段, got %v", v.Stripped)
	}
}

// read_canvas 的列表视图：标量给、机器字段不给、数组对象不给（它们归详情层）。
func TestVisibleScalarSettings(t *testing.T) {
	raw := json.RawMessage(`{"modelId":"minimax-h3","ratio":"16:9","history":[1,2],"pendingGen":{"a":1},"scriptRowId":"r1","videoPrompt":"hello"}`)
	got := visibleScalarSettings("gen-video", raw)
	if got["modelId"] != "minimax-h3" {
		t.Errorf("modelId 必须出现在列表视图——「哪些节点用的什么模型」要在列表层就能判断: %v", got)
	}
	// ratio 不在静态清单里（它是模型 uiSchema 字段），但 gen-* 的清单外标量
	// 照样回传——列表层要能看出「哪些节点比例不对」。
	if got["ratio"] != "16:9" {
		t.Errorf("动态字段的当前值应回传: %v", got)
	}
	if got["scriptRowId"] != "r1" {
		t.Errorf("readonly 标量也该可见: %v", got)
	}
	if _, ok := got["history"]; ok {
		t.Error("machine 字段不该回传")
	}
	if _, ok := got["pendingGen"]; ok {
		t.Error("machine 字段不该回传")
	}
}

func TestVisibleScalarSettingsDropsArraysAndObjects(t *testing.T) {
	raw := json.RawMessage(`{"rows":[{"id":"a"}],"assets":{"characters":[]},"scriptPrompt":"go"}`)
	got := visibleScalarSettings("script", raw)
	if _, ok := got["rows"]; ok {
		t.Error("数组归详情层，展开会把 200 节点画布的列表输出撑爆")
	}
	if _, ok := got["assets"]; ok {
		t.Error("对象归详情层")
	}
	if got["scriptPrompt"] != "go" {
		t.Errorf("标量应保留: %v", got)
	}
}

// 清单里没有的字段不回传：存量快照里躺着废弃字段，回传等于邀请 agent 去写一个
// 没人读的字段——正是本次要根治的那类静默失败。
// 没有动态字段的类型严格按清单过滤：存量快照里躺着废弃字段，回传等于邀请
// agent 去写一个没人读的字段——正是本次要根治的那类静默失败。
func TestVisibleScalarSettingsDropsUnknownFields(t *testing.T) {
	raw := json.RawMessage(`{"legacyThing":"x","text":"hi"}`)
	got := visibleScalarSettings("text-node", raw)
	if _, ok := got["legacyThing"]; ok {
		t.Error("清单外字段不该回传")
	}
	if got["text"] != "hi" {
		t.Error("清单内字段应回传")
	}
}

func TestRejectionMessageNamesTheTool(t *testing.T) {
	msg := rejectionMessage(UpdateNodeToolName, []string{"字段 x 不存在。"})
	if !strings.Contains(msg, "未执行") {
		t.Errorf("回执必须明说没有执行，否则 planner 会当成功: %s", msg)
	}
	if !strings.Contains(msg, UpdateNodeToolName) {
		t.Errorf("回执要点名工具: %s", msg)
	}
}
