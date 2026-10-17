package generation

import (
	"log"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/lifecycle"
)

// tracker 返回在途任务的 goroutine 登记处：未注入时用进程级 lifecycle.Default。
// 字段注入只给测试用——Default 是全局的，测试里 BeginDrain 会污染同包其他用例。
func (s *Service) tracker() *lifecycle.Tracker {
	if s.taskTracker != nil {
		return s.taskTracker
	}
	return lifecycle.Default
}

// draining：进程正在关闭，不再领取/接管新任务（已领取的照常跑完或留给下次启动续跑）。
func (s *Service) draining() bool { return s.tracker().Draining() }

// Drain 是关闭时的排空：先停止领取新任务，再最多等 timeout 让在途任务收尾。
// 返回 false 表示超时仍有任务在跑——它们的状态由下次启动的租约过期 + 接管续上。
func (s *Service) Drain(timeout time.Duration) bool {
	tr := s.tracker()
	tr.BeginDrain()
	ok := tr.Wait(timeout)
	if !ok {
		log.Printf("[gen] 关闭排空超时(%s)，仍有在途任务，下次启动续跑", timeout)
	}
	return ok
}
