package canvasgen

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SettingCanvasMultiAngle 是「绘图多角度设置」在 settings 表里的 key。
// 与绘图提示词预设(gen_image_prompt_presets)平级、互不复用——多角度的条目
// 形状是强类型的(角度三元组)，不需要预设那套自由 JSON 扩展位。
const SettingCanvasMultiAngle = "canvas_multi_angle"

// MultiAnglePresetKey 是多角度生成走 SubmitInput.PresetKey 通道时用的保留 key。
// 复用这条通道的原因：拼接必须收口在 generation 执行层（GET /tasks 会原样吐回
// input 快照，提前拼=泄露模板明文——与绘图预设同一条保密铁律），而执行层的
// resolver 钩子已经存在，借道即可零改 generation 层。注意绘图预设的 key regex
// 也放行下划线，管理员理论上能配出同名预设——所以 Enqueue 与 resolver 都必须
// **先于**绘图预设查表分流本 key，保证它永远走多角度分支。
const MultiAnglePresetKey = "__multi_angle__"

// 景别三档，key 与 liblib 对标(zoomCloseUp/zoomMedium/zoomPanorama)。
var multiAngleZoomPhrases = map[string]string{
	"closeup":  "特写构图",
	"medium":   "中景构图",
	"panorama": "全景构图",
}

// MultiAngleZoomKeys 后台校验与前端滑杆共用的档位序(近→远)。
var MultiAngleZoomKeys = []string{"closeup", "medium", "panorama"}

// DefaultMultiAngleTemplate 是自定义角度拼接模板的缺省值。{angle} 会被替换成
// 「水平/垂直/景别」三段拼好的中文短语。放常量而不硬编码在 Compose 里，
// 后台清空模板字段时行为可预期。
const DefaultMultiAngleTemplate = "改变相机视角：{angle}。保持画面主体、服装、场景与光线不变，生成同一场景的新视角图片"

// MultiAnglePreset 一条预设(如「鱼眼视角」)。Prompt 是附加提示词，对前端保密；
// 角度三元组会下发给前端(用于点预设时滑杆跳位)，不属于保密内容。
type MultiAnglePreset struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Rotation int    `json:"rotation"` // 水平环绕 0~359，顺时针(向右)
	Tilt     int    `json:"tilt"`     // 垂直俯仰，正=俯拍 负=仰拍
	Zoom     string `json:"zoom"`     // closeup|medium|panorama
	Prompt   string `json:"prompt"`   // 附加提示词，可空(纯角度预设)；保密
}

// MultiAngleConfig 整份配置。Models 是启用此功能的图片模型(顺序即前端下拉顺序，
// 第一个是默认选中)；Template 是角度短语的外层模板(空=用默认)。
type MultiAngleConfig struct {
	Models   []string           `json:"models"`
	Template string             `json:"template"`
	Presets  []MultiAnglePreset `json:"presets"`
}

// LoadMultiAngleConfig 读配置；未配置返回空配置不报错(同 LoadGenImagePresetsConfig)。
func LoadMultiAngleConfig(get func(key string) (string, error)) (MultiAngleConfig, error) {
	raw, err := get(SettingCanvasMultiAngle)
	if err != nil {
		return MultiAngleConfig{}, err
	}
	if raw == "" {
		return MultiAngleConfig{}, nil
	}
	var cfg MultiAngleConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return MultiAngleConfig{}, err
	}
	return cfg, nil
}

// Preset 按 key 查预设；查不到 ok=false，调用方按「无附加词」降级(同绘图预设：
// 后台删预设不能让存量节点重生成失败)。
func (c MultiAngleConfig) Preset(key string) (MultiAnglePreset, bool) {
	if key == "" {
		return MultiAnglePreset{}, false
	}
	for _, p := range c.Presets {
		if p.Key == key {
			return p, true
		}
	}
	return MultiAnglePreset{}, false
}

// multiAngleAtoi 容错取整：params 走 map[string]string 通道，非法值按 0(不动)处理
// 而不是报错——执行层没有把错误退给用户的通道，宁可少拼一段也别让任务失败。
func multiAngleAtoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// ComposeMultiAngleText 把角度参数拼成发往上游的中文前置文本。
// params 契约(全 string，走 PresetParams 通道)：
//   - "rotation" 水平环绕角(任意整数，内部归一到 0~359)
//   - "tilt"     垂直俯仰角(正=俯拍 负=仰拍)
//   - "zoom"     closeup|medium|panorama
//   - "preset"   可选，预设 key——只用于附加该预设的保密 prompt；
//     角度一律以数值参数为准(用户点完预设还能再拖滑杆微调)。
//
// 返回空串表示「无可拼内容」(角度全零且无预设词)，调用方按未选预设处理。
func (c MultiAngleConfig) ComposeMultiAngleText(params map[string]string) string {
	rotation := ((multiAngleAtoi(params["rotation"]) % 360) + 360) % 360
	tilt := multiAngleAtoi(params["tilt"])

	parts := []string{}
	switch {
	case rotation == 0:
		// 不动，不写
	case rotation == 180:
		parts = append(parts, "将镜头转到主体正后方，从背面拍摄")
	case rotation < 180:
		parts = append(parts, fmt.Sprintf("将镜头水平向右环绕%d度", rotation))
	default:
		parts = append(parts, fmt.Sprintf("将镜头水平向左环绕%d度", 360-rotation))
	}
	switch {
	case tilt > 0:
		parts = append(parts, fmt.Sprintf("从上方俯拍%d度", tilt))
	case tilt < 0:
		parts = append(parts, fmt.Sprintf("从下方仰拍%d度", -tilt))
	}
	if phrase := multiAngleZoomPhrases[params["zoom"]]; phrase != "" {
		parts = append(parts, phrase)
	}

	angleText := ""
	if len(parts) > 0 {
		tpl := c.Template
		if strings.TrimSpace(tpl) == "" {
			tpl = DefaultMultiAngleTemplate
		}
		angleText = strings.ReplaceAll(tpl, "{angle}", strings.Join(parts, "，"))
	}

	presetPrompt := ""
	if p, ok := c.Preset(params["preset"]); ok {
		presetPrompt = strings.TrimSpace(p.Prompt)
	}

	switch {
	case angleText != "" && presetPrompt != "":
		return angleText + "\n" + presetPrompt
	case angleText != "":
		return angleText
	default:
		return presetPrompt
	}
}
