package canvasgen

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
)

// captureGen 记录 Enqueue 透传给 generation.Submit 的入参。
type captureGen struct {
	last generation.SubmitInput
}

func (c *captureGen) Submit(_ context.Context, in generation.SubmitInput) (*generation.SubmitResult, error) {
	c.last = in
	return &generation.SubmitResult{TaskID: 42}, nil
}
func (c *captureGen) Get(context.Context, int64, int64) (*generation.Task, error) { return nil, nil }
func (c *captureGen) LatestByCanvasNode(context.Context, int64, int64, string) (*generation.Task, error) {
	return nil, nil
}

func presetTestService(t *testing.T, configJSON string) (*Service, *captureGen) {
	t.Helper()
	gen := &captureGen{}
	svc := NewService(gen, nil)
	svc.SetSettingsGetter(func(key string) (string, error) {
		if key != SettingGenImagePresets {
			t.Fatalf("意外读了别的 settings key: %s", key)
		}
		return configJSON, nil
	})
	return svc, gen
}

const presetCfgJSON = `{"models":["m1"],"presets":[
	{"key":"three_view","value":{"name":"角色三视图","prompt":"白底三视图","minImages":1,"maxImages":1}},
	{"key":"free_style","value":{"name":"自由风格","prompt":"随便画"}}]}`

// 图片数命中约束 → presetKey 原样透传给 generation（正文替换在执行层，这里只带 key）。
func TestEnqueuePresetKeyPassthrough(t *testing.T) {
	svc, gen := presetTestService(t, presetCfgJSON)
	_, err := svc.Enqueue(context.Background(), EnqueueInput{
		UserID: 1, ProjectID: 2, NodeID: "n1", ModelID: "m1", PresetKey: "three_view",
		Inputs: map[string]any{"prompt": "画只猫", "images": []any{"https://a/1.png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gen.last.PresetKey != "three_view" {
		t.Fatalf("PresetKey = %q", gen.last.PresetKey)
	}
	// inputs 原样：拼接绝不能发生在提交层（任务快照会被 GET /tasks 原样吐回）。
	if gen.last.Inputs["prompt"] != "画只猫" {
		t.Fatalf("提交层不许改 prompt: %v", gen.last.Inputs["prompt"])
	}
}

// 图片数不满足 → PresetConstraintError（handler 据此回 400），文案用 name 不用 key。
func TestEnqueuePresetImageBoundsRejected(t *testing.T) {
	svc, _ := presetTestService(t, presetCfgJSON)
	cases := []struct {
		name   string
		inputs map[string]any
	}{
		{"零张", map[string]any{"prompt": "x"}},
		{"两张", map[string]any{"prompt": "x", "images": []any{"a", "b"}}},
	}
	for _, c := range cases {
		_, err := svc.Enqueue(context.Background(), EnqueueInput{
			UserID: 1, ProjectID: 2, NodeID: "n1", ModelID: "m1", PresetKey: "three_view", Inputs: c.inputs,
		})
		var pce *PresetConstraintError
		if !errors.As(err, &pce) {
			t.Fatalf("%s: 应拒，got %v", c.name, err)
		}
		if !strings.Contains(pce.Error(), "角色三视图") || strings.Contains(pce.Error(), "three_view") {
			t.Fatalf("文案要用 name 不用 key: %s", pce.Error())
		}
	}
}

// key 在配置里查不到（后台已删）→ 静默按未选处理，不拦生成、不把悬空 key 落快照。
func TestEnqueuePresetKeyMissingDegrades(t *testing.T) {
	svc, gen := presetTestService(t, presetCfgJSON)
	_, err := svc.Enqueue(context.Background(), EnqueueInput{
		UserID: 1, ProjectID: 2, NodeID: "n1", ModelID: "m1", PresetKey: "deleted_key",
		Inputs: map[string]any{"prompt": "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gen.last.PresetKey != "" {
		t.Fatalf("悬空 key 不该落快照: %q", gen.last.PresetKey)
	}
}

// 无约束的预设不数图片；未装配 settingsGet 时 presetKey 也原样透传（执行层 resolver 兜底）。
func TestEnqueuePresetNoBoundsAndNoGetter(t *testing.T) {
	svc, gen := presetTestService(t, presetCfgJSON)
	if _, err := svc.Enqueue(context.Background(), EnqueueInput{
		UserID: 1, ProjectID: 2, NodeID: "n1", ModelID: "m1", PresetKey: "free_style",
		Inputs: map[string]any{"prompt": "x"},
	}); err != nil {
		t.Fatal(err)
	}
	if gen.last.PresetKey != "free_style" {
		t.Fatalf("PresetKey = %q", gen.last.PresetKey)
	}

	bare := NewService(gen, nil) // 未 SetSettingsGetter
	if _, err := bare.Enqueue(context.Background(), EnqueueInput{
		UserID: 1, ProjectID: 2, NodeID: "n1", ModelID: "m1", PresetKey: "whatever",
		Inputs: map[string]any{"prompt": "x"},
	}); err != nil {
		t.Fatal(err)
	}
	if gen.last.PresetKey != "whatever" {
		t.Fatalf("未装配时应原样透传: %q", gen.last.PresetKey)
	}
}

// imageInputCount 认三种入参形状：images []any（网页 JSON）、[]string（CLI Go 侧）、单数 image。
func TestImageInputCountShapes(t *testing.T) {
	cases := []struct {
		inputs map[string]any
		want   int
	}{
		{map[string]any{"images": []any{"a", "b"}}, 2},
		{map[string]any{"images": []string{"a"}}, 1},
		{map[string]any{"image": "a"}, 1},
		{map[string]any{"image": ""}, 0},
		{map[string]any{}, 0},
	}
	for i, c := range cases {
		if got := imageInputCount(c.inputs); got != c.want {
			t.Errorf("case %d: got %d want %d", i, got, c.want)
		}
	}
}

// 约束文案的四种形态。
func TestPresetImageCountViolationWording(t *testing.T) {
	mk := func(v string) GenImagePresetEntry {
		var e GenImagePresetEntry
		if err := json.Unmarshal([]byte(`{"key":"k","value":`+v+`}`), &e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	cases := []struct {
		value string
		count int
		want  string
	}{
		{`{"name":"A","prompt":"p","minImages":1,"maxImages":1}`, 0, "恰好 1 张"},
		{`{"name":"A","prompt":"p","minImages":1,"maxImages":3}`, 4, "1-3 张"},
		{`{"name":"A","prompt":"p","minImages":2}`, 1, "至少 2 张"},
		{`{"name":"A","prompt":"p","maxImages":0}`, 1, "至多 0 张"},
		{`{"name":"A","prompt":"p","minImages":1}`, 1, ""},
	}
	for i, c := range cases {
		got := presetImageCountViolation(mk(c.value), c.count)
		if c.want == "" && got != "" {
			t.Errorf("case %d: 应满足，got %q", i, got)
		}
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("case %d: got %q want contains %q", i, got, c.want)
		}
	}
}
