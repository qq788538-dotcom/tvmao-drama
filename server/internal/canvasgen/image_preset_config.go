package canvasgen

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SettingGenImagePresets 是「绘图提示词预设」在 settings 表里的 key。
// gen-image 节点专属——视频将来另起 gen_video_prompt_presets，互不复用，
// 两套配置天然隔离。
const SettingGenImagePresets = "gen_image_prompt_presets"

// GenImagePresetEntry 是一条预设。Value 刻意用 map 而非结构体：key-value + 自由 JSON
// 是有意的扩展位设计——未知字段必须原样落库、原样回显（admin 整体覆盖写回时不能丢），
// 结构体会把没声明的字段静默吃掉。
type GenImagePresetEntry struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

// GenImagePresetsConfig 是整份配置。Models 是「整套绑模型」：节点当前选中模型命中
// 列表才显示预设菜单——这只是前端体验层，服务端按 key 解析预设时不看它。
// Presets 用数组不用 map：map 序列化丢顺序，数组顺序就是后台排的展示顺序
// （分类顺序 = category 首次出现的先后，也依赖这个数组序）。
type GenImagePresetsConfig struct {
	Models  []string              `json:"models"`
	Presets []GenImagePresetEntry `json:"presets"`
}

// LoadGenImagePresetsConfig 读配置；空(未配置)返回空配置不报错（同 LoadCanvasPostprocessConfig）。
func LoadGenImagePresetsConfig(get func(key string) (string, error)) (GenImagePresetsConfig, error) {
	raw, err := get(SettingGenImagePresets)
	if err != nil {
		return GenImagePresetsConfig{}, err
	}
	if raw == "" {
		return GenImagePresetsConfig{}, nil
	}
	var cfg GenImagePresetsConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return GenImagePresetsConfig{}, err
	}
	return cfg, nil
}

// Preset 按 key 找条目。查不到 ok=false——所有调用方一律按「未选预设」静默降级
// 而不是报错：后台删掉一条预设，不该让还存着旧 key 的存量节点从此生成失败。
func (c GenImagePresetsConfig) Preset(key string) (GenImagePresetEntry, bool) {
	if key == "" {
		return GenImagePresetEntry{}, false
	}
	for _, p := range c.Presets {
		if p.Key == key {
			return p, true
		}
	}
	return GenImagePresetEntry{}, false
}

// str 从 value 里取字符串字段；缺失/类型不对回空串（value 是自由 JSON，不能假设形状）。
func (e GenImagePresetEntry) str(field string) string {
	s, _ := e.Value[field].(string)
	return s
}

// Prompt 是要拼进上游请求的提示词正文——对前端保密，只有服务端执行层读它。
func (e GenImagePresetEntry) Prompt() string { return e.str("prompt") }

// Name 是用户可见的显示名。报错文案一律用它不用 key（用户看不懂 key）。
func (e GenImagePresetEntry) Name() string { return e.str("name") }

// ImageBounds 返回该预设对上游连线图片数的约束；hasMin/hasMax=false 表示该边界未配置。
// JSON 反序列化进 map 后数字统一是 float64，负数按未配置处理（后台校验本不放行，兜底）。
func (e GenImagePresetEntry) ImageBounds() (min int, hasMin bool, max int, hasMax bool) {
	if v, ok := e.Value["minImages"].(float64); ok && v >= 0 {
		min, hasMin = int(v), true
	}
	if v, ok := e.Value["maxImages"].(float64); ok && v >= 0 {
		max, hasMax = int(v), true
	}
	return
}

// GenImagePresetParamOption 是参数维度里的一个固定档位。Prompt 是该档位对应的
// 提示词片段——与预设正文同级的保密内容，只在服务端执行层出现。
type GenImagePresetParamOption struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Prompt  string `json:"prompt"`
	Default bool   `json:"default"`
}

// GenImagePresetParam 是一个参数维度（如「皮肤」），Options 是它的固定档位单选集。
type GenImagePresetParam struct {
	Key     string                      `json:"key"`
	Name    string                      `json:"name"`
	Options []GenImagePresetParamOption `json:"options"`
}

// Params 解析条目的参数定义（value.params，选填）。value 是自由 JSON，
// 经 marshal/unmarshal 一轮拿到强类型；解析不动或缺失一律返回 nil（无参数预设）。
func (e GenImagePresetEntry) Params() []GenImagePresetParam {
	raw, ok := e.Value["params"]
	if !ok {
		return nil
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var params []GenImagePresetParam
	if json.Unmarshal(blob, &params) != nil {
		return nil
	}
	return params
}

// defaultOption 返回维度的默认档位：显式标 default 的第一个，否则第一个档位。
func (p GenImagePresetParam) defaultOption() (GenImagePresetParamOption, bool) {
	for _, o := range p.Options {
		if o.Default {
			return o, true
		}
	}
	if len(p.Options) > 0 {
		return p.Options[0], true
	}
	return GenImagePresetParamOption{}, false
}

// ComposePresetText 拼出该预设发往上游的完整前置文本：正文 + 各参数维度选中
// 档位的片段（按维度配置顺序，换行分隔）。selected 是「维度 key → 档位 key」，
// 缺失/非法的维度落默认档位——前端只传用户显式选过的，服务端兜底 default，
// 两侧不必对「什么算选了」较劲。片段为空的档位跳过（允许「无效果」档位）。
func (e GenImagePresetEntry) ComposePresetText(selected map[string]string) string {
	parts := []string{}
	if p := e.Prompt(); p != "" {
		parts = append(parts, p)
	}
	for _, dim := range e.Params() {
		opt, ok := dim.defaultOption()
		if want, exists := selected[dim.Key]; exists {
			for _, o := range dim.Options {
				if o.Key == want {
					opt, ok = o, true
					break
				}
			}
		}
		if ok && opt.Prompt != "" {
			parts = append(parts, opt.Prompt)
		}
	}
	return strings.Join(parts, "\n")
}

// PresetConstraintError 表示预设的图片数约束不满足。独立类型让 handler 层能
// errors.As 出来回 400（连线是用户自己能改的前置条件，不是网关故障）——
// 与 generation.InputLimitError 同一套处理思路。文案用预设 name 不用 key：
// key 是内部标识，用户看不懂。
type PresetConstraintError struct{ Msg string }

func (e *PresetConstraintError) Error() string { return e.Msg }

// imageInputCount 数生成入参里的输入图片张数。inputs 的形状由前端
// buildImageGenerationInputs / 服务端 canvasinputs.BuildImageInputs 两侧同构产出：
// 有连线图片时带 images 数组（网页 JSON 化后是 []any，CLI 的 Go 侧是 []string），
// 部分老形状只有单数 image 字段。
func imageInputCount(inputs map[string]any) int {
	switch v := inputs["images"].(type) {
	case []any:
		return len(v)
	case []string:
		return len(v)
	}
	if s, _ := inputs["image"].(string); s != "" {
		return 1
	}
	return 0
}

// presetImageCountViolation 对照预设的 min/max 检查图片数；返回空串 = 满足，
// 非空 = 面向用户的拒绝文案。
func presetImageCountViolation(p GenImagePresetEntry, count int) string {
	min, hasMin, max, hasMax := p.ImageBounds()
	if (!hasMin || count >= min) && (!hasMax || count <= max) {
		return ""
	}
	want := ""
	switch {
	case hasMin && hasMax && min == max:
		want = fmt.Sprintf("恰好 %d 张", min)
	case hasMin && hasMax:
		want = fmt.Sprintf("%d-%d 张", min, max)
	case hasMin:
		want = fmt.Sprintf("至少 %d 张", min)
	default:
		want = fmt.Sprintf("至多 %d 张", max)
	}
	return fmt.Sprintf("预设「%s」需要%s输入图片，当前 %d 张", p.Name(), want, count)
}
