package canvasgen

import (
	"strings"
	"testing"
)

func lcCfg() LightControlConfig {
	b := 100
	return LightControlConfig{
		Models: []string{"m1", "m2"},
		Presets: []LightControlPreset{
			{Key: "rembrandt", Name: "伦勃朗光", Direction: "high-front-left"}, // 纯参数无附加词
			{Key: "overexposed", Name: "过曝胶片", Brightness: &b, Prompt: "柯达胶片质感"},
			{Key: "cyberpunk", Name: "赛博朋克", Prompt: "赛博朋克霓虹打光"},
		},
	}
}

func TestComposeLightControlText(t *testing.T) {
	cfg := lcCfg()
	for _, tc := range []struct {
		name     string
		params   map[string]string
		contains []string
		absent   []string
	}{
		{
			name:     "方向+亮度+颜色+轮廓光全量",
			params:   map[string]string{"keyLight": "high-front-left", "brightness": "80", "lightColor": "#2d34fa", "rimLight": "back"},
			contains: []string{"左前上方约45度", "伦勃朗式", "亮度约80%", "明亮的高调光", "主光色调为蓝色", "正后方的轮廓光"},
		},
		{
			name:     "脏标记:缺 key 的组一律不拼",
			params:   map[string]string{"keyLight": "left"},
			contains: []string{"正左侧", "侧光"},
			absent:   []string{"亮度", "色调", "轮廓光", "参考"},
		},
		{
			name:     "低亮度走低调光文案",
			params:   map[string]string{"brightness": "10"},
			contains: []string{"亮度约10%", "低调光"},
			absent:   []string{"高调光"},
		},
		{
			name:     "中亮度不带氛围定语",
			params:   map[string]string{"brightness": "50"},
			contains: []string{"亮度约50%"},
			absent:   []string{"低调光", "高调光"},
		},
		{
			name:     "颜色映射最近色名:纯白",
			params:   map[string]string{"lightColor": "#ffffff"},
			contains: []string{"主光色调为白色"},
		},
		{
			name:     "轮廓光未知方向按back容错",
			params:   map[string]string{"rimLight": "sideways"},
			contains: []string{"正后方的轮廓光"},
		},
		{
			name:     "参考图追加迁移指令",
			params:   map[string]string{"hasRef": "1"},
			contains: []string{"参考另一张输入图片的打光风格"},
		},
		{
			name:     "预设保密词拼在参数文本后",
			params:   map[string]string{"keyLight": "top", "preset": "cyberpunk"},
			contains: []string{"顶光", "赛博朋克霓虹打光"},
		},
		{
			name:     "非法方向/亮度/颜色全部静默跳过",
			params:   map[string]string{"keyLight": "sideways", "brightness": "abc", "lightColor": "blue"},
			absent:   []string{"主光源", "亮度", "色调"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := cfg.ComposeLightControlText(tc.params)
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

// 26 方向词表全覆盖：每个枚举都必须有非空短语（前端球面吸附依赖这份全集）。
func TestLightDirectionPhrasesComplete(t *testing.T) {
	if len(lightDirectionPhrases) != 26 {
		t.Fatalf("方向词表应为 26 档，got %d", len(lightDirectionPhrases))
	}
	for _, k := range LightDirectionKeys {
		if strings.TrimSpace(lightDirectionPhrases[k]) == "" {
			t.Fatalf("方向 %s 缺短语", k)
		}
	}
}

// 全空参数 + 无预设词 → 空串（调用方按未选预设处理，不给上游拼空模板）。
func TestComposeLightControlTextEmpty(t *testing.T) {
	cfg := lcCfg()
	if got := cfg.ComposeLightControlText(map[string]string{}); got != "" {
		t.Fatalf("期望空串，got: %s", got)
	}
	// 悬空预设 key（后台已删）同样降级为空，不报错。
	if got := cfg.ComposeLightControlText(map[string]string{"preset": "deleted"}); got != "" {
		t.Fatalf("悬空预设应为空串，got: %s", got)
	}
	// 纯参数预设（无附加词）且未带显式参数：也为空（参数以显式入参为准）。
	if got := cfg.ComposeLightControlText(map[string]string{"preset": "rembrandt"}); got != "" {
		t.Fatalf("无词预设+无参数应为空串，got: %s", got)
	}
}

// 后台自定义模板生效；空模板落默认模板。
func TestComposeLightControlTemplate(t *testing.T) {
	cfg := lcCfg()
	cfg.Template = "光影：{light}！"
	got := cfg.ComposeLightControlText(map[string]string{"keyLight": "top"})
	if got != "光影：主光源来自正上方，顶光！" {
		t.Fatalf("模板未生效: %s", got)
	}
	cfg.Template = "  "
	got = cfg.ComposeLightControlText(map[string]string{"keyLight": "top"})
	if !strings.Contains(got, "主光源来自正上方") || !strings.Contains(got, "保持画面主体") {
		t.Fatalf("空模板应落默认: %s", got)
	}
}

// Enqueue 分流依赖:保留 key 不在预设表里也不能被剥掉——钉住常量形状防手滑改名。
func TestLightControlPresetKeyReserved(t *testing.T) {
	if LightControlPresetKey != "__light_control__" {
		t.Fatalf("保留 key 变更会让存量节点快照失效: %s", LightControlPresetKey)
	}
}
