// Package lifecycle 提供进程优雅关闭的最小设施：追踪后台长任务 goroutine + 跛鸭(draining)标志。
// 只依赖标准库，不 import 任何业务包，避免循环依赖；业务侧用包级 Go() 包装后台 goroutine。
package lifecycle

import (
	"sync"
	"sync/atomic"
	"time"
)

type Tracker struct {
	wg       sync.WaitGroup
	draining atomic.Bool
}

// Default 是进程级单例。业务代码用包级 Go()/Draining() 即作用于它。
var Default = &Tracker{}

// Go 启动一个受 drain 追踪的后台 goroutine——drain 时 Wait 会等它结束。
func (t *Tracker) Go(fn func()) {
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		fn()
	}()
}

// BeginDrain 置跛鸭：之后 Draining() 返回 true(healthz 据此 503)。
func (t *Tracker) BeginDrain()    { t.draining.Store(true) }
func (t *Tracker) Draining() bool { return t.draining.Load() }

// Wait 等所有受追踪 goroutine 结束或超时。true=全部结束, false=超时(剩余靠后续阶段恢复)。
func (t *Tracker) Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// 包级便捷函数：作用于 Default。
func Go(fn func())                    { Default.Go(fn) }
func BeginDrain()                     { Default.BeginDrain() }
func Draining() bool                  { return Default.Draining() }
func Wait(timeout time.Duration) bool { return Default.Wait(timeout) }
