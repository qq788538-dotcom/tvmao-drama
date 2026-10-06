package canvasgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// snapshotSearcher 是 projects repo 的窄子集:按内容找快照。与 snapshotMutator 分开
// 定义、用 SetSnapshotSearcher 单独注入,不并进 snapshotMutator——那会牵动所有测试
// fake,而且只有补搬这一个调用方需要它。
type snapshotSearcher interface {
	SnapshotProjectsContaining(substr string) ([]int64, error)
}

func (s *Service) SetSnapshotSearcher(sr snapshotSearcher) { s.search = sr }

// RewriteMediaRef 实现 generation.MediaRefRewriter:把所有画布快照里的旧 URL 换成新的。
// 节点 content 与 settings.history[].url 存的是同一串,字符串替换一次改完,不解析节点
// 结构——URL 不含引号/反斜杠,替换后仍是合法 JSON。走 MutateSnapshot 行锁事务,与
// writeNode 同一条写路径;改完广播 version、op 为 nil,观看端整拉一次快照(一期行为),
// 开着页面的用户不用刷新。未注入 searcher 时是 no-op:补搬照常,只是画布副本不改。
func (s *Service) RewriteMediaRef(_ context.Context, userID int64, oldURL, newURL string) error {
	if s.search == nil || oldURL == "" || oldURL == newURL {
		return nil
	}
	pids, err := s.search.SnapshotProjectsContaining(oldURL)
	if err != nil {
		return fmt.Errorf("search snapshots: %w", err)
	}
	old, next := []byte(oldURL), []byte(newURL)
	for _, pid := range pids {
		// MutateSnapshot 没改动时也返回当前 version,不能拿 version>0 当「改了」——
		// 那会让没命中的画布(LIKE 匹配到别的字段、或用户已换图)白拉一次快照。
		changed := false
		version, err := s.snaps.MutateSnapshot(pid, func(payload json.RawMessage) (json.RawMessage, bool, error) {
			if len(payload) == 0 || !bytes.Contains(payload, old) {
				return payload, false, nil
			}
			changed = true
			return bytes.ReplaceAll(payload, old, next), true, nil
		})
		if err != nil {
			return fmt.Errorf("rewrite snapshot project=%d: %w", pid, err)
		}
		if changed && s.notifier != nil {
			s.notifier.NotifyCanvasChanged(userID, pid, version, "server", nil)
		}
	}
	return nil
}
