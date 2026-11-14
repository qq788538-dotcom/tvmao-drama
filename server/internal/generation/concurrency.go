package generation

import "sync"

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

func (l *modelLimiter) acquire() {
	l.mu.Lock()
	for l.active >= l.limit {
		l.cond.Wait()
	}
	l.active++
	l.mu.Unlock()
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
}

func NewConcurrencyRegistry() *ConcurrencyRegistry {
	return &ConcurrencyRegistry{byModel: map[string]*modelLimiter{}}
}

// Acquire 取一个该模型的并发名额(必要时按 limit 刷新容量),返回归还函数。
// 名额要持有到整个生成生命周期结束(提交→出结果)才 release——由调用方 defer。
func (r *ConcurrencyRegistry) Acquire(modelID string, limit int) func() {
	r.mu.Lock()
	l := r.byModel[modelID]
	if l == nil {
		l = newModelLimiter(limit)
		r.byModel[modelID] = l
	}
	r.mu.Unlock()
	l.setLimit(limit) // 跟随最新配置热调该模型上限
	l.acquire()
	var once sync.Once
	return func() { once.Do(l.release) }
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
