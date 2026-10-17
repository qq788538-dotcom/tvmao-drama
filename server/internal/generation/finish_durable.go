package generation

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3"
)

// finishRetryDelays：成功收口遇到瞬时错误（SQLITE_BUSY 等）时的退避间隔，次数即重试上限。
// 包级变量便于测试置零。
var finishRetryDelays = []time.Duration{500 * time.Millisecond, 1 * time.Second, 2 * time.Second}

// finishAttemptTimeout：单次收口事务的上限。比 busy_timeout(10s) 略长，让 SQLite 自己的
// 忙等先跑完再判超时。
const finishAttemptTimeout = 15 * time.Second

// finishSuccessDurable 是同步/异步 finalize 的成功收口。
//
// 走到这里时上游额度已经花掉、产物已经写盘建了 asset 行：收口失败会让任务停在 running，
// 被接管器判成"结果无法确认"标失败。所以：
//   - 不继承执行 ctx 的取消（关闭、30 分钟队列超时都不该丢结果），每次尝试用独立超时；
//   - SQLITE_BUSY / locked / 单次超时（含语句执行中途超时被 SQLite 中断）这类瞬时错误有界退避重试；
//   - 执行 ctx 被主动取消（关闭流程、心跳失租）时只做手头这一次，不再重试：关闭不该被退避拖住，
//     失租了重试也只会 CAS 落空；30 分钟队列超时是 DeadlineExceeded，不算主动取消，照常重试；
//   - 失租（won=false, err=nil）和其他错误立刻返回。
func (s *Service) finishSuccessDurable(ctx context.Context, taskID, lease int64, out finishOutcome) (bool, error) {
	fn := s.finishSuccessHook
	if fn == nil {
		fn = s.finishSuccessTx
	}
	base := context.WithoutCancel(ctx)
	for attempt := 0; ; attempt++ {
		actx, cancel := context.WithTimeout(base, finishAttemptTimeout)
		won, err := fn(actx, taskID, lease, out)
		cancel()
		if err == nil || !isTransientDBError(err) || attempt >= len(finishRetryDelays) || errors.Is(ctx.Err(), context.Canceled) {
			if err != nil {
				log.Printf("[gen-finish] task=%d 成功收口失败(尝试 %d 次): %v; 已持久化产物=%s",
					taskID, attempt+1, err, persistedURLsForLog(out.sinkRes.OutputURLs))
			}
			return won, err
		}
		log.Printf("[gen-finish] task=%d 成功收口瞬时失败，%s 后重试(%d/%d): %v",
			taskID, finishRetryDelays[attempt], attempt+1, len(finishRetryDelays), err)
		time.Sleep(finishRetryDelays[attempt])
	}
}

// isTransientDBError：写锁争用、单次尝试超时等重试可能成功的错误。
//
// ncruces 驱动下，语句执行中途 ctx 到期会被 sqlite3_interrupt 打断，返回的是 sqlite3.INTERRUPT
// （"sqlite3: interrupted"），不带 context.DeadlineExceeded，所以要按错误码认。
// 主动取消（context.Canceled）不是瞬时错误。
func isTransientDBError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, sqlite3.BUSY) || errors.Is(err, sqlite3.LOCKED) || errors.Is(err, sqlite3.INTERRUPT) {
		return true
	}
	msg := err.Error()
	for _, k := range []string{"SQLITE_BUSY", "SQLITE_LOCKED", "database is locked", "database table is locked"} {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

// persistedURLsForLog 只打本地 media:// 地址，便于收口失败后手动恢复；其余形态只记个数
// （外链可能带签名、data: 可能很大）。
func persistedURLsForLog(urls []string) string {
	local := make([]string, 0, len(urls))
	for _, u := range urls {
		if strings.HasPrefix(u, "media://") {
			local = append(local, u)
		}
	}
	return fmt.Sprintf("[%s] (+%d 个非本地)", strings.Join(local, ","), len(urls)-len(local))
}
