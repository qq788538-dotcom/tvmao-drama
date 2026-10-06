package generation

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

// 业务重试（v5 §4.2）：同一任务原地重试，一笔冻结贯穿始终。
//
// 只有「上游权威终态失败」能走到这里——上游协议明确宣告任务死亡（轮询返回配置的
// failed/cancelled），重排不存在重复扣费风险，清空上游标记是安全的。
// 与「接管重排」（进程被杀、结论不确定，只有 upstream_called_at IS NULL 才能排）
// 是两条完全不同的路径，判据不同、能清的字段不同，混淆会直接导致重复扣费（v5 §2）。

// upstreamTerminalError 标记上游权威终态失败。pollUpstream 在轮询状态命中配置的
// failed/cancelled 值时返回它；其他一切错误形态（transport 超时/5xx/解析失败/本地
// 轮询超时）都不是它——那些是"不确定"，走保守退款，绝不清标记重排。
type upstreamTerminalError struct{ msg string }

func (e *upstreamTerminalError) Error() string { return e.msg }

// defaultRetryIntervalSec 是 gen_control 未配置重试间隔时的兜底（与旧 canvas 层一致）。
const defaultRetryIntervalSec = 30

// retryModuleForSink 把 sink kind 映射到 gen_control.modules 的配置键。
// 画布 → canvas；drama 系全部 → drama；无 sink/未知 → ""（不重试）。
func retryModuleForSink(sink json.RawMessage) string {
	if len(sink) == 0 {
		return ""
	}
	var ref struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(sink, &ref); err != nil {
		return ""
	}
	switch {
	case ref.Kind == "canvas_node":
		return "canvas"
	case strings.HasPrefix(ref.Kind, "drama_"):
		return "drama"
	default:
		return ""
	}
}

// tryScheduleRetry 尝试把权威终态失败的任务排进 retry_pending。
// 返回 true = 已排（钱不动、不通知发起方——这不是终态）；false = 不该重试或没抢到
// （sinkless / 未配重试 / 次数耗尽 / 状态已被别人推进），调用方走终败路径。
//
// 上限与"是否有 sink"的判定都压进 MarkRetryPending 的单条 UPDATE 里（WHERE 带
// sink IS NOT NULL AND attempt < ?）——判定与转移原子，不存在读-判-写窗口。
func (s *Service) tryScheduleRetry(taskID, lease int64, model *models.ModelConfig, sink json.RawMessage, errMsg string) bool {
	rc := model.GenRetry(retryModuleForSink(sink))
	if rc.MaxRetries <= 0 {
		return false
	}
	interval := rc.RetryIntervalSec
	if interval <= 0 {
		interval = defaultRetryIntervalSec
	}
	won, err := s.repo.MarkRetryPending(taskID, lease, rc.MaxRetries, interval, errMsg)
	if err != nil {
		log.Printf("[gen-retry] task=%d 排重试失败: %v", taskID, err)
		return false
	}
	if won {
		log.Printf("[gen-retry] task=%d 上游权威失败，%ds 后重试: %s", taskID, interval, errMsg)
	}
	return won
}
