package cliapi

import "encoding/json"

// ChangeNotifier 接收 CLI 的画布写入事件，由 sidecar 的事件中心实现。
// 只有 CLI 写入才发：生成回写、Agent、网页自己的写入走各自已有的链路，不经过这里。
type ChangeNotifier interface {
	Publish(projectID, version int64, source string)
}

// SourceCLI 是事件里的 source 字段值。
const SourceCLI = "cli"

type notifyingStore struct {
	ProjectStore
	n ChangeNotifier
}

// WithChangeNotifier 包一层 ProjectStore：MutateSnapshot 提交成功且确有改动时发事件。
// 包在存储层而不是逐个写方法里调用，新增写操作时不会漏发。
func WithChangeNotifier(s ProjectStore, n ChangeNotifier) ProjectStore {
	if n == nil {
		return s
	}
	return notifyingStore{ProjectStore: s, n: n}
}

func (s notifyingStore) MutateSnapshot(projectID int64, fn func(payload json.RawMessage) (json.RawMessage, bool, error)) (int64, error) {
	changed := false
	v, err := s.ProjectStore.MutateSnapshot(projectID, func(p json.RawMessage) (json.RawMessage, bool, error) {
		out, ch, err := fn(p)
		changed = ch && err == nil
		return out, ch, err
	})
	if err == nil && changed && v > 0 {
		s.n.Publish(projectID, v, SourceCLI)
	}
	return v, err
}
