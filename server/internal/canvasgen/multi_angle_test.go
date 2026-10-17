package canvasgen

import (
	"strings"
	"testing"
)

func maCfg() MultiAngleConfig {
	return MultiAngleConfig{
		Models: []string{"m1", "m2"},
		Presets: []MultiAnglePreset{
			{Key: "fisheye", Name: "鱼眼视角", Rotation: 0, Tilt: 30, Zoom: "closeup", Prompt: "鱼眼畸变"},
			{Key: "front-down", Name: "正面俯拍", Rotation: 0, Tilt: 60, Zoom: "medium"}, // 纯角度无附加词
		},
	}
}

func TestComposeMultiAngleText(t *testing.T) {
	cfg := maCfg()
	for _, tc := range []struct {
		name     string
		params   map[string]string
		contains []string
		absent   []string
	}{
		{
			name:     "向右环绕+俯拍+中景",
			params:   map[string]string{"rotation": "45", "tilt": "30", "zoom": "medium"},
			contains: []string{"水平向右环绕45度", "俯拍30度", "中景构图"},
		},
		{
			name:     "大于180走向左",
			params:   map[string]string{"rotation": "300", "tilt": "-15", "zoom": "panorama"},
			contains: []string{"水平向左环绕60度", "仰拍15度", "全景构图"},
			absent:   []string{"向右"},
		},
		{
			name:     "180=背面",
			params:   map[string]string{"rotation": "180", "tilt": "0"},
			contains: []string{"正后方", "背面"},
			absent:   []string{"俯拍", "仰拍"},
		},
		{
			name:     "负角度归一(-90=270=向左90)",
			params:   map[string]string{"rotation": "-90"},
			contains: []string{"水平向左环绕90度"},
		},
		{
			name:     "预设附加词拼在角度文本后",
			params:   map[string]string{"rotation": "0", "tilt": "30", "zoom": "closeup", "preset": "fisheye"},
			contains: []string{"俯拍30度", "特写构图", "鱼眼畸变"},
		},
		{
			name:     "非法数值按0容错,zoom非法跳过",
			params:   map[string]string{"rotation": "abc", "tilt": "", "zoom": "bogus", "preset": "front-down"},
			contains: []string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := cfg.ComposeMultiAngleText(tc.params)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Fatalf("缺少 %q，got: %s", want, got)
				}
			}
			for _, no := range tc.absent {
				if strings.Contains(got, no) {
					t.Fatalf("不该出现 %q，got: %s", no, got)
				}
			}
		})
	}
}

// 全零角度 + 无预设词 → 空串（调用方按未选预设处理，不给上游拼空模板）。
func TestComposeMultiAngleTextEmpty(t *testing.T) {
	cfg := maCfg()
	if got := cfg.ComposeMultiAngleText(map[string]string{"rotation": "0", "tilt": "0"}); got != "" {
		t.Fatalf("期望空串，got: %s", got)
	}
	// 悬空预设 key（后台已删）同样降级为空，不报错。
	if got := cfg.ComposeMultiAngleText(map[string]string{"preset": "deleted"}); got != "" {
		t.Fatalf("悬空预设应为空串，got: %s", got)
	}
	// 纯角度预设（无附加词）但角度为零：也为空。
	if got := cfg.ComposeMultiAngleText(map[string]string{"preset": "front-down"}); got != "" {
		t.Fatalf("无词预设+零角度应为空串，got: %s", got)
	}
}

// 后台自定义模板生效；空模板落默认模板。
func TestComposeMultiAngleTemplate(t *testing.T) {
	cfg := maCfg()
	cfg.Template = "视角变化：{angle}！"
	got := cfg.ComposeMultiAngleText(map[string]string{"rotation": "90"})
	if got != "视角变化：将镜头水平向右环绕90度！" {
		t.Fatalf("模板未生效: %s", got)
	}
	cfg.Template = "  "
	got = cfg.ComposeMultiAngleText(map[string]string{"rotation": "90"})
	if !strings.Contains(got, "将镜头水平向右环绕90度") || !strings.Contains(got, "保持画面主体") {
		t.Fatalf("空模板应落默认: %s", got)
	}
}

// Enqueue 分流依赖:保留 key 不在预设表里也不能被剥掉——钉住常量形状防手滑改名。
func TestMultiAnglePresetKeyReserved(t *testing.T) {
	if MultiAnglePresetKey != "__multi_angle__" {
		t.Fatalf("保留 key 变更会让存量节点快照失效: %s", MultiAnglePresetKey)
	}
}
