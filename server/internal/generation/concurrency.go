package generation

import (
	"context"
	"errors"
	"sync"
)

// errSlotWaitAborted：等模型名额期间被叫停（失租或进程关闭）。调用方不动钱、静默退出。
var errSlotWaitAborted = errors.New("等待并发名额时被中止")

// modelLimiter 是可运行期动态调容量的并发信号量(mutex+cond)。
// channel 容量不可变,而这里上限要随模型配置热调,故自己实现。
// 语义是"每模型一把"(由 ConcurrencyRegistry 持有 map)。
type modelLimiter struct {
	mu     sync.Mutex
	cond   *sync.Cond
	active int
	limit  int
}

func newModelLimiter(limit int) *modelLimiter {
	if limit < 1 {
		limit = 1
	}
	l := &modelLimiter{limit: limit}
	l.cond = sync.NewCond(&l.mu)
	return l
}

func (l *modelLimiter) setLimit(n int) {
	if n < 1 {
		n = 1
	}
	l.mu.Lock()
	changed := l.limit != n
	l.limit = n
	l.mu.Unlock()
	if changed {
		l.cond.Broadcast()
	}
}

// acquireAbortable 等到名额就占一个（active++）；abort 结束时放弃等待并返回 errSlotWaitAborted
// （abort 为 nil 时永不放弃）。cond.Wait 不能 select，所以名额不够时挂一个 AfterFunc：abort 一结束
// 就持锁 Broadcast，把等待者叫醒复查。持锁再 Broadcast 保证不会漏在「复查之后、Wait 之前」那一刻。
func (l *modelLimiter) acquireAbortable(abort context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active < l.limit {
		l.active++
		return nil
	}
	if abort != nil {
		stop := context.AfterFunc(abort, func() {
			l.mu.Lock()
			l.cond.Broadcast()
			l.mu.Unlock()
		})
		defer stop()
	}
	for l.active >= l.limit {
		// 只在仍然没名额时放弃：被 release 的 Signal 叫醒时名额已空出，照常占用，
		// 不会把这次唤醒吞掉而让别的等待者干等。
		if abort != nil && abort.Err() != nil {
			return errSlotWaitAborted
		}
		l.cond.Wait()
	}
	l.active++
	return nil
}

func (l *modelLimiter) release() {
	l.mu.Lock()
	if l.active > 0 {
		l.active--
	}
	l.mu.Unlock()
	l.cond.Signal()
}

// ConcurrencyRegistry 是"每模型一把并发闸"的共享持有者。
//
// 设计:并发闸不塞进 Submit 内部,而是由业务 runner(画布 canvasgen、drama)
// 在自己的"占位同步跑"goroutine 里 Acquire → 跑完整个生命周期 → release。全平台只 new 一个
// ConcurrencyRegistry 实例、同时传给画布和 drama,于是**同一模型跨模块共用同一把信号量**——
// 这就是"并发是模型属性、跨模块共享"的落地点。
//
// 容量来源:调用方(手里有 ModelConfig)传 model.GenConcurrency()(已应用默认 200),每次 Acquire
// 前刷新该模型的上限,天然跟随后台配置热调,无需单独 refresher。
type ConcurrencyRegistry struct {
	mu      sync.Mutex
	byModel map[string]*modelLimiter
	// persisting：本进程里「生成名额已还、任务行仍在跑」（落盘阶段）的任务数，按模型计。
	// 队列领取按 DB 里 running/processing/settling 的行数算空位，这些行也数在里面；
	// 领取时扣掉它们，下载慢才不会拖住队列任务的领取（P20，见 persistAwarePickupRepo）。
	persisting map[string]int
}

func NewConcurrencyRegistry() *ConcurrencyRegistry {
	return &ConcurrencyRegistry{byModel: map[string]*modelLimiter{}, persisting: map[string]int{}}
}

// BeginPersist 记一笔「落盘中」（见 persisting），返回结束函数（sync.Once 包装）。
// 调用方在名额归还之后、落盘开始时调用，落盘结束（终态收口之前）调结束函数。
func (r *ConcurrencyRegistry) BeginPersist(modelID string) func() {
	r.mu.Lock()
	if r.persisting == nil {
		r.persisting = map[string]int{}
	}
	r.persisting[modelID]++
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			if r.persisting[modelID]--; r.persisting[modelID] <= 0 {
				delete(r.persisting, modelID)
			}
			r.mu.Unlock()
		})
	}
}

// PersistingByModel 返回各模型落盘中任务数的快照。
func (r *ConcurrencyRegistry) PersistingByModel() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.persisting))
	for k, v := range r.persisting {
		out[k] = v
	}
	return out
}

// Acquire 取一个该模型的并发名额(必要时按 limit 刷新容量),返回归还函数。
// 名额要持有到整个生成生命周期结束(提交→出结果)才 release——由调用方 defer。
func (r *ConcurrencyRegistry) Acquire(modelID string, limit int) func() {
	release, _ := r.AcquireAbortable(modelID, limit, nil) // abort 为 nil 不会失败
	return release
}

// AcquireAbortable 同 Acquire，但 abort 结束时放弃等待，返回 errSlotWaitAborted（此时没有占名额）。
// 归还函数同样由 sync.Once 包装：提前归还（上游返回即还，见 callAndComplete）与兜底 defer
// 各调一次也只释放一次。
func (r *ConcurrencyRegistry) AcquireAbortable(modelID string, limit int, abort context.Context) (func(), error) {
	r.mu.Lock()
	l := r.byModel[modelID]
	if l == nil {
		l = newModelLimiter(limit)
		r.byModel[modelID] = l
	}
	r.mu.Unlock()
	l.setLimit(limit) // 跟随最新配置热调该模型上限
	if err := l.acquireAbortable(abort); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(l.release) }, nil
}

// Occupy 不阻塞地占一个该模型的名额(只 active++,可以超出上限),返回归还函数。
//
// 只给孤儿续跑用:续跑的任务在上个进程里本就占着名额、上游正在跑,重启后必须让同模型的
// 新任务看到它们,否则新任务还能再开满一轮。续跑路径**不能**阻塞在 Acquire 上——心跳起来
// 之前阻塞,周期性 recover 会再次接管同一个任务;心跳之后阻塞,又会吃掉剩余的轮询窗口。
// 所以只记账不等待,下载峰值交给全局下载闸(downloadSem)。超出上限期间新的 Acquire 照常等待。
func (r *ConcurrencyRegistry) Occupy(modelID string) func() {
	r.mu.Lock()
	l := r.byModel[modelID]
	if l == nil {
		// 容量由该模型下一次 Acquire 按配置刷新,这里先按兜底默认值建。
		l = newModelLimiter(defaultModelConcurrency)
		r.byModel[modelID] = l
	}
	r.mu.Unlock()
	l.mu.Lock()
	l.active++
	l.mu.Unlock()
	var once sync.Once
	return func() { once.Do(l.release) }
}
