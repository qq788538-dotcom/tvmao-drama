package http

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/agent"
)

func TestModelPatchToEdits(t *testing.T) {
	name, c, r, iv, so := "新名", 2, 3, 10, 5
	en := false
	edits, err := modelPatchToEdits(ModelPatch{
		Name: &name, Enabled: &en, SortOrder: &so, Concurrency: &c, CanvasMaxRetries: &r, CanvasRetryIntervalSec: &iv,
		UISchemaDefaults: map[string]json.RawMessage{"ratio": json.RawMessage(`"16:9"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for k, v := range edits {
		got[k] = string(v)
	}
	want := map[string]string{
		"name": `"新名"`, "enabled": `false`, "sort_order": `5`, "gen_control.concurrency": `2`,
		"gen_control.modules.canvas.maxRetries": `3`, "gen_control.modules.canvas.retryIntervalSec": `10`,
		"ui_schema.ratio.default": `"16:9"`,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %s, want %s", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("多余字段: %v", got)
	}
	if _, err := modelPatchToEdits(ModelPatch{UISchemaDefaults: map[string]json.RawMessage{"a.b": json.RawMessage(`1`)}}); err == nil {
		t.Fatal("参数名含非法字符应报错")
	}
}

func TestOverrideToPatchField(t *testing.T) {
	for in, want := range map[string]string{
		"name": "name", "sort_order": "sortOrder", "ui_schema.ratio.default": "uiSchemaDefaults",
		"gen_control.concurrency": "concurrency", "gen_control.modules.canvas.maxRetries": "canvasMaxRetries",
		"gen_control.modules.canvas.retryIntervalSec": "canvasRetryIntervalSec", "enabled": "enabled", "hidden": "hidden",
	} {
		if got := overrideToPatchField(in); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}

func TestSettingEditsOnlyChangedEditablePaths(t *testing.T) {
	var cur, next any
	json.Unmarshal([]byte(`{"models":["a"],"template":"t {angle}","presets":[{"key":"fisheye","name":"鱼眼","prompt":"旧"},{"key":"top","name":"俯拍","prompt":"不变"}]}`), &cur)
	json.Unmarshal([]byte(`{"models":["a","b"],"template":"t {angle}","presets":[{"key":"fisheye","name":"改名被忽略","prompt":"新"},{"key":"top","name":"俯拍","prompt":"不变"}]}`), &next)
	edits, err := settingEdits([]string{"models", "template", "presets[*].prompt"}, cur, next)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for k := range edits {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "models,presets[fisheye].prompt" {
		t.Fatalf("edits=%v", keys)
	}
	if string(edits["presets[fisheye].prompt"]) != `"新"` || string(edits["models"]) != `["a","b"]` {
		t.Fatalf("%s %s", edits["presets[fisheye].prompt"], edits["models"])
	}
}

func TestSettingEditsNestedKeyedArrays(t *testing.T) {
	var cur, next any
	json.Unmarshal([]byte(`{"presets":[{"key":"p","value":{"prompt":"x","params":[{"key":"skin","options":[{"key":"n","prompt":""}]}]}}]}`), &cur)
	json.Unmarshal([]byte(`{"presets":[{"key":"p","value":{"prompt":"x","params":[{"key":"skin","options":[{"key":"n","prompt":"自然"}]}]}}]}`), &next)
	edits, _ := settingEdits([]string{"presets[*].value.prompt", "presets[*].value.params[*].options[*].prompt"}, cur, next)
	if len(edits) != 1 || string(edits["presets[p].value.params[skin].options[n].prompt"]) != `"自然"` {
		t.Fatalf("%v", edits)
	}
}

type memBackbone struct{ rows map[int64]agent.BackboneModel }

func (m *memBackbone) List() ([]agent.BackboneModel, error) {
	out := []agent.BackboneModel{}
	for _, r := range m.rows {
		out = append(out, r)
	}
	return out, nil
}
func (m *memBackbone) SetModelName(id int64, name string) error {
	r, ok := m.rows[id]
	if !ok {
		return agent.ErrBackboneNotFound
	}
	r.ModelName = name
	m.rows[id] = r
	return nil
}
func (m *memBackbone) ResetModelName(id int64, def string) error { return m.SetModelName(id, def) }

func TestBackboneAgentModelsAdapter(t *testing.T) {
	mem := &memBackbone{rows: map[int64]agent.BackboneModel{1: {ID: 1, Role: "planner", Alias: "主Agent", ModelName: "canvas-agent-gpt-5.5", Temperature: 0.3}}}
	a := &backboneAgentModels{list: mem.List, sw: mem, defaults: func(id int64) (string, bool) { return "canvas-agent-gpt-5.5", id == 1 }}
	ctx := context.Background()
	if err := a.SwitchAgentModel(ctx, 1, "gpt-5.5"); err != nil {
		t.Fatal(err)
	}
	items, _ := a.ListAgentModels(ctx)
	if items[0].ModelName != "gpt-5.5" || !items[0].Modified || mem.rows[1].Temperature != 0.3 {
		t.Fatalf("%+v %+v", items, mem.rows[1])
	}
	if err := a.RestoreAgentModelDefault(ctx, 1); err != nil || mem.rows[1].ModelName != "canvas-agent-gpt-5.5" {
		t.Fatalf("恢复默认失败: %v", err)
	}
	if err := a.SwitchAgentModel(ctx, 9, "x"); !errors.Is(err, ErrSettingsNotFound) {
		t.Fatalf("应映射为 ErrSettingsNotFound: %v", err)
	}
	if err := a.RestoreAgentModelDefault(ctx, 9); !errors.Is(err, ErrSettingsNotFound) {
		t.Fatalf("无默认值应 ErrSettingsNotFound: %v", err)
	}
}
