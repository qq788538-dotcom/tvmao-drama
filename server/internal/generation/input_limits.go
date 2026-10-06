package generation

import (
	"fmt"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

// InputLimitError：提交的输入媒体数量违反模型 gen_control.inputLimits 约束。
// 独立类型而非 sentinel var——文案携带动态的 N/M，errors.Is 匹配不了；HTTP 层
// errors.As 后映射 400（网页）/ INVALID_ARGUMENT（CLI）。归到 502/可重试都是错的：
// 重试改变不了画布上的连线数量。
type InputLimitError struct{ Msg string }

func (e *InputLimitError) Error() string { return e.Msg }

// mediaKinds：inputLimits 键 → (inputs 数组字段名, 用户可见名)。只认这三类——
// 不认识的约束键静默忽略（spec 2026-08-12 扩展性语义：新键先配置、后升级二进制
// 期间约束不生效而不是报错，运维要知道这一点）。
var mediaKinds = []struct{ kind, inputsKey, label string }{
	{"video", "videos", "视频"},
	{"image", "images", "图片"},
	{"audio", "audios", "音频"},
}

// validateInputLimits 按模型 inputLimits 对归一化后的 inputs 做数量校验。
// 与 validateInputsAgainstUISchema 同梯队：在建行/冻结之前拒绝，事务内零写入。
// limits 为空（未配置）直接放行——存量模型零开销。
func validateInputLimits(limits map[string]models.InputLimit, inputs map[string]any) error {
	if len(limits) == 0 {
		return nil
	}
	for _, mk := range mediaKinds {
		lim, ok := limits[mk.kind]
		if !ok {
			continue
		}
		n := countMediaInputs(inputs[mk.inputsKey])
		if lim.Max != nil && n > *lim.Max {
			if *lim.Max == 0 {
				return &InputLimitError{Msg: fmt.Sprintf("该模型不支持连接%s", mk.label)}
			}
			return &InputLimitError{Msg: fmt.Sprintf(
				"该模型最多支持连接 %d 个%s，当前连接了 %d 个", *lim.Max, mk.label, n)}
		}
		if lim.Min != nil && n < *lim.Min {
			return &InputLimitError{Msg: fmt.Sprintf(
				"该模型需要连接至少 %d 个%s", *lim.Min, mk.label)}
		}
	}
	return nil
}

// countMediaInputs 数某个媒体数组的长度。网页提交经 JSON 反序列化是 []any，
// CLI 路径（canvasinputs.BuildVideoInputs）构造的是 []string——两种都要认，
// 只认一种会让另一条路的约束整个失效。缺失/其他类型按 0；空串元素不计。
func countMediaInputs(v any) int {
	n := 0
	switch arr := v.(type) {
	case []any:
		for _, it := range arr {
			if s, ok := it.(string); ok && s == "" {
				continue
			}
			n++
		}
	case []string:
		for _, s := range arr {
			if s != "" {
				n++
			}
		}
	}
	return n
}
