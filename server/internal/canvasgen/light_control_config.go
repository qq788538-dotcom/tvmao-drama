package canvasgen

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SettingCanvasLightControl 是「绘图打光设置」在 settings 表里的 key。
// 与多角度(canvas_multi_angle)平级、互不复用——两个功能各自的预设形状不同
// (打光预设是「参数组合+保密词+参考图」的自由组合)，混在一份配置里删改会互伤。
const SettingCanvasLightControl = "canvas_light_control"

// LightControlPresetKey 是打光生成走 SubmitInput.PresetKey 通道时用的保留 key。
// 借道理由与 __multi_angle__ 完全一致：拼接必须收口在 generation 执行层
// (GET /tasks 会原样吐回 input 快照，提前拼=泄露模板明文)，resolver 钩子现成。
// Enqueue 与 resolver 都必须**先于**绘图预设查表分流本 key。
const LightControlPresetKey = "__light_control__"

// lightDirectionPhrases 26 方向 → 中文打光短语（词表：
// 8 水平方位 × 3 高度 + top/bottom）。key 用小写连字符，与前端球面吸附枚举一致。
// 文案面向通用图生图模型的指令跟随写（摄影术语+方位），跑图不满意直接改这里，
// 排队中的任务执行时即生效（resolver 每次现读 settings，但短语表在代码里——
// 改文案要发版；只有模板和预设词后台可热改。词表进代码是刻意的：枚举与前端
// 球面吸附一一对应，配置化会让两端词表漂移）。
var lightDirectionPhrases = map[string]string{
	"front":            "主光源来自正前方，顺光",
	"front-right":      "主光源来自右前方",
	"right":            "主光源来自正右侧，侧光，明暗对比分明",
	"back-right":       "主光源来自右后方，侧逆光",
	"back":             "主光源来自正后方，逆光，主体边缘发亮",
	"back-left":        "主光源来自左后方，侧逆光",
	"left":             "主光源来自正左侧，侧光，明暗对比分明",
	"front-left":       "主光源来自左前方",
	"high-front":       "主光源来自前上方约45度，经典人像布光",
	"high-front-right": "主光源来自右前上方约45度，伦勃朗式布光",
	"high-right":       "主光源来自右上方",
	"high-back-right":  "主光源来自右后上方，逆光轮廓",
	"high-back":        "主光源来自后上方，逆光轮廓",
	"high-back-left":   "主光源来自左后上方，逆光轮廓",
	"high-left":        "主光源来自左上方",
	"high-front-left":  "主光源来自左前上方约45度，伦勃朗式布光",
	"low-front":        "主光源来自前下方，底光上打",
	"low-front-right":  "主光源来自右前下方，低位补光",
	"low-right":        "主光源来自右下方",
	"low-back-right":   "主光源来自右后下方，低位逆光",
	"low-back":         "主光源来自后下方，低位逆光",
	"low-back-left":    "主光源来自左后下方，低位逆光",
	"low-left":         "主光源来自左下方",
	"low-front-left":   "主光源来自左前下方，低位补光",
	"top":              "主光源来自正上方，顶光",
	"bottom":           "主光源来自正下方，底光，戏剧化阴影",
}

// lightRimPhrases 轮廓光方向 → 中文短语。词表只有背面
// 9 向（轮廓光的物理意义就是从背面勾边）。当前前端只发 "back"（开关无方向选择），
// 其余键位为将来放开方向选择留的词——收到未知值按缺省
// back 容错，不让任务失败。
var lightRimPhrases = map[string]string{
	"back":            "正后方",
	"back-left":       "左后方",
	"back-right":      "右后方",
	"high-back":       "后上方",
	"high-back-left":  "左后上方",
	"high-back-right": "右后上方",
	"low-back":        "后下方",
	"low-back-left":   "左后下方",
	"low-back-right":  "右后下方",
}

// LightDirectionKeys 26 方向枚举全集（校验用）。顺序无语义，仅为遍历稳定。
var LightDirectionKeys = func() []string {
	keys := make([]string, 0, len(lightDirectionPhrases))
	for k := range lightDirectionPhrases {
		keys = append(keys, k)
	}
	return keys
}()

// lightColorRefs hex 主光色 → 最近中文色名的参照点。模型读 hex 不稳，翻成色名；
// 参照点刻意稀疏（基础色相环+黑白），近似到「模型能理解的颜色词」就够了，
// 精确色值本来就不是文生图能承诺的粒度。
var lightColorRefs = []struct {
	name    string
	r, g, b int
}{
	{"白色", 255, 255, 255},
	{"暖黄色", 255, 200, 120},
	{"红色", 255, 0, 0},
	{"橙色", 255, 128, 0},
	{"黄色", 255, 255, 0},
	{"绿色", 0, 200, 80},
	{"青色", 0, 255, 255},
	{"蓝色", 40, 60, 255},
	{"紫色", 140, 0, 255},
	{"粉色", 255, 105, 180},
	{"黑色", 0, 0, 0},
}

var lightHexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// nearestLightColorName hex → 最近参照色名；非法 hex 返回空串（调用方跳过该段）。
func nearestLightColorName(hex string) string {
	if !lightHexRe.MatchString(hex) {
		return ""
	}
	r, _ := strconv.ParseInt(hex[1:3], 16, 32)
	g, _ := strconv.ParseInt(hex[3:5], 16, 32)
	b, _ := strconv.ParseInt(hex[5:7], 16, 32)
	best, bestDist := "", 1<<62
	for _, c := range lightColorRefs {
		dr, dg, db := int(r)-c.r, int(g)-c.g, int(b)-c.b
		d := dr*dr + dg*dg + db*db
		if d < bestDist {
			best, bestDist = c.name, d
		}
	}
	return best
}

// DefaultLightControlTemplate 打光拼接模板缺省值。{light} 会被替换成
// 「方向/亮度/颜色/轮廓光/参考图」各段拼好的中文短语。
const DefaultLightControlTemplate = "调整画面打光：{light}。保持画面主体、构图与内容不变，仅改变光照方向与明暗氛围"

// lightRefPhrase 带打光参考图时追加的迁移指令（参考图本身走 images 第二张下发）。
const lightRefPhrase = "参考另一张输入图片的打光风格，将其光影氛围迁移到本图"

// LightControlPreset 一条打光预设(如「伦勃朗光」)。参数部分（direction/brightness/
// lightColor/referenceImage/thumbnail）会下发给前端（点预设时表单跳位/带图），
// 不属于保密内容——这些参数本就体现在界面上；只有 Prompt 保密。
// 一条预设可任意组合三类内容：纯参数（伦勃朗光）、纯保密词（赛博朋克）、
// 参数+词混合（过曝胶片）、参考图（落日迷幻），字段留空即「该维度不动」。
type LightControlPreset struct {
	Key            string `json:"key"`
	Name           string `json:"name"`
	Thumbnail      string `json:"thumbnail"`      // 预设网格缩略图 URL，空=前端退化文字 chip
	Direction      string `json:"direction"`      // 26 方向枚举之一，空=不设方向
	Brightness     *int   `json:"brightness"`     // 0-100；指针区分「未设置」与「设为 0」
	LightColor     string `json:"lightColor"`     // #rrggbb，空=不设颜色
	ReferenceImage string `json:"referenceImage"` // 打光参考图 URL，空=无
	Prompt         string `json:"prompt"`         // 附加提示词，保密，可空
}

// LightControlConfig 整份配置。Models 是启用打光的图片模型（顺序即前端默认，
// 第一个默认选中）；Template 是打光短语的外层模板（空=用默认）。
type LightControlConfig struct {
	Models   []string             `json:"models"`
	Template string               `json:"template"`
	Presets  []LightControlPreset `json:"presets"`
}

// LoadLightControlConfig 读配置；未配置返回空配置不报错(同 LoadMultiAngleConfig)。
func LoadLightControlConfig(get func(key string) (string, error)) (LightControlConfig, error) {
	raw, err := get(SettingCanvasLightControl)
	if err != nil {
		return LightControlConfig{}, err
	}
	if raw == "" {
		return LightControlConfig{}, nil
	}
	var cfg LightControlConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return LightControlConfig{}, err
	}
	return cfg, nil
}

// Preset 按 key 查预设；查不到 ok=false，调用方按「无附加词」降级（后台删预设
// 不能让存量节点重生成失败）。
func (c LightControlConfig) Preset(key string) (LightControlPreset, bool) {
	if key == "" {
		return LightControlPreset{}, false
	}
	for _, p := range c.Presets {
		if p.Key == key {
			return p, true
		}
	}
	return LightControlPreset{}, false
}

// ComposeLightControlText 把打光参数拼成发往上游的中文前置文本。
// params 契约（全 string，走 PresetParams 通道，**缺 key = 用户没碰该组 = 不拼**，
// 这是脏标记语义：缺省交给模型自由发挥，不发默认值捆住模型）：
//   - "keyLight"   26 方向枚举（未知值跳过不报错——执行层没有把错误退给用户的通道）
//   - "brightness" "0"~"100"（非法值跳过）
//   - "lightColor" "#rrggbb"（非法值跳过）
//   - "rimLight"   轮廓光方向（背面 9 向；未知值按 back 容错）
//   - "preset"     可选，预设 key——只用于附加该预设的保密 prompt；
//     参数一律以显式入参为准（点完预设还能再改控件，与多角度同规则）
//   - "hasRef"     "1" = 带打光参考图（图走 images 第二张，这里只追加迁移指令）
//
// 返回空串表示「无可拼内容」，调用方按未选预设处理。
func (c LightControlConfig) ComposeLightControlText(params map[string]string) string {
	parts := []string{}
	if phrase := lightDirectionPhrases[params["keyLight"]]; phrase != "" {
		parts = append(parts, phrase)
	}
	if raw := strings.TrimSpace(params["brightness"]); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n <= 100 {
			switch {
			case n < 30:
				parts = append(parts, fmt.Sprintf("整体光照亮度约%d%%，昏暗的低调光氛围", n))
			case n > 70:
				parts = append(parts, fmt.Sprintf("整体光照亮度约%d%%，明亮的高调光", n))
			default:
				parts = append(parts, fmt.Sprintf("整体光照亮度约%d%%", n))
			}
		}
	}
	if name := nearestLightColorName(params["lightColor"]); name != "" {
		parts = append(parts, "主光色调为"+name)
	}
	if rim, ok := params["rimLight"]; ok && rim != "" {
		dir := lightRimPhrases[rim]
		if dir == "" {
			dir = lightRimPhrases["back"]
		}
		parts = append(parts, "为主体添加来自"+dir+"的轮廓光，勾勒边缘轮廓")
	}
	if params["hasRef"] == "1" {
		parts = append(parts, lightRefPhrase)
	}

	lightText := ""
	if len(parts) > 0 {
		tpl := c.Template
		if strings.TrimSpace(tpl) == "" {
			tpl = DefaultLightControlTemplate
		}
		lightText = strings.ReplaceAll(tpl, "{light}", strings.Join(parts, "，"))
	}

	presetPrompt := ""
	if p, ok := c.Preset(params["preset"]); ok {
		presetPrompt = strings.TrimSpace(p.Prompt)
	}

	switch {
	case lightText != "" && presetPrompt != "":
		return lightText + "\n" + presetPrompt
	case lightText != "":
		return lightText
	default:
		return presetPrompt
	}
}
