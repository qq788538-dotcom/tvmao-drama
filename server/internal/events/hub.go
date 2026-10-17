// Package events 是 sidecar 进程内的项目变更事件中心：CLI 写画布后发布，
// 网页经 SSE 订阅，空闲时整拉快照。只保证"尽力送达"：订阅者缓冲满了直接丢，前端每次
// （重）连上都会比较一次版本号，丢掉的事件靠这一步补回来。
package events

import "sync"

type Event struct {
	ProjectID int64  `json:"projectId"`
	Version   int64  `json:"version"`
	Source    string `json:"source"`
}

const bufferSize = 16

type Hub struct {
	mu     sync.Mutex
	subs   map[int64]map[chan Event]struct{}
	closed bool
	done   chan struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[int64]map[chan Event]struct{}{}, done: make(chan struct{})}
}

func (h *Hub) Publish(projectID, version int64, source string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	e := Event{ProjectID: projectID, Version: version, Source: source}
	for ch := range h.subs[projectID] {
		select {
		case ch <- e:
		default: // 慢订阅者：丢弃，不阻塞 CLI 的写请求
		}
	}
}

// Subscribe 返回事件 channel 和退订函数（幂等）。Hub 已关闭时返回一个已关闭的 channel。
func (h *Hub) Subscribe(projectID int64) (<-chan Event, func()) {
	ch := make(chan Event, bufferSize)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(ch)
		return ch, func() {}
	}
	if h.subs[projectID] == nil {
		h.subs[projectID] = map[chan Event]struct{}{}
	}
	h.subs[projectID][ch] = struct{}{}
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if _, ok := h.subs[projectID][ch]; ok {
				delete(h.subs[projectID], ch)
				close(ch)
			}
		})
	}
}

// Close 关闭全部订阅并让 Done 可读。sidecar 退出时调用：srv.Shutdown 不会取消在途请求的 ctx，
// SSE handler 靠 Done 及时返回，否则每次退出都要等满 shutdownTimeout。
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for _, set := range h.subs {
		for ch := range set {
			close(ch)
		}
	}
	h.subs = nil
	close(h.done)
}

func (h *Hub) Done() <-chan struct{} { return h.done }
