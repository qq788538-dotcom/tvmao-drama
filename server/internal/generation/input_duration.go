package generation

import (
	"context"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

// InputDurationError：配置了 gen_control.inputDuration 的模型缺少输入视频。独立类型
// （同 InputLimitError 模式）：HTTP 层 errors.As 映射 400，用户连上视频即可自解。
type InputDurationError struct{ Msg string }

func (e *InputDurationError) Error() string { return e.Msg }

// FirstVideoInput 取 inputs.videos 第一个非空字符串。网页路径是 []any、CLI 路径是 []string。
func FirstVideoInput(inputs map[string]any) string {
	switch arr := inputs["videos"].(type) {
	case []any:
		for _, it := range arr {
			if s, ok := it.(string); ok && s != "" {
				return s
			}
		}
	case []string:
		for _, s := range arr {
			if s != "" {
				return s
			}
		}
	}
	return ""
}

// resolveInputDuration 按 gen_control.inputDuration 从首个输入视频取真实时长注入 inputs.duration。
//
// 桌面端不计费、不带 ffprobe，上游请求模板也不读这个值（它只影响产物 assets.duration_ms），
// 所以只走 duration_ms 快路径：查到就注入，查不到放行。
// 不做下载 + 探测 + 回填还有一个硬理由：SubmitTx 在写事务里调用本函数，回填经 r.db 写库会
// 自死锁（等 busy_timeout 10 秒后 SQLITE_BUSY），读几百 MB 文件也会长时间占着写锁。
func (s *Service) resolveInputDuration(_ context.Context, cfg *models.InputDurationConfig, inputs map[string]any) error {
	if cfg == nil {
		return nil
	}
	src := FirstVideoInput(inputs)
	if src == "" {
		return &InputDurationError{Msg: "该模型需要连接视频输入"}
	}
	if s.storage == nil || s.assets == nil {
		return nil
	}
	key, ok := s.storage.ExtractKey(src)
	if !ok || key == "" {
		return nil
	}
	ms, found, err := s.assets.GetDurationMsByObjectKey(key)
	if err != nil || !found || ms <= 0 {
		return nil
	}
	inputs["duration"] = float64(ms) / 1000.0
	return nil
}
