package agent

import (
	"sync"
	"time"
)

// agentMicroLayout 是 agent_messages.created_at / summarized_at 的库内格式（UTC，定宽 26 字符）。
// 定宽让 SQLite 的文本比较等价于时间比较，keyset 分页直接比库内原文。
const agentMicroLayout = "2006-01-02 15:04:05.000000"

var (
	microMu   sync.Mutex
	microLast time.Time
)

// agentMicroNow 返回严格单调递增的微秒时间文本：同一微秒内的第二次调用加 1µs，
// 保证同一进程里先写的消息排序一定在前（参考仓靠 MySQL NOW(6)，同刻时只能靠 id 碰运气）。
func agentMicroNow() string {
	microMu.Lock()
	defer microMu.Unlock()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !now.After(microLast) {
		now = microLast.Add(time.Microsecond)
	}
	microLast = now
	return now.Format(agentMicroLayout)
}
