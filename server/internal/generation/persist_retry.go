package generation

import (
	"context"
	"log"
	"strings"
	"time"
)

// persistRetryDelays 是单个产物槽位下载/落盘失败后的重试间隔（失败自动重试 3 次）。
// 包级变量而非 Service 字段：测试置零即可，生产不需要配置。
var persistRetryDelays = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}

func isRemoteURL(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// persistSlotWithRetry 在 persistSlot 外包一层重试。只重试 http(s) 产物——data: 解码失败、
// file:// 读失败都是确定性的，重试没有意义。http(s) 也只重试瞬时错误（网络错误、408/429/5xx、
// 存储写失败）；其余 4xx 和超过大小上限直接返回（isRetryablePersistError）。重试全部失败时
// 返回最后一次的错误，由 persistOutputs 保留上游地址（任务照常成功，节点显示「未保存到本地」）。
func (s *Service) persistSlotWithRetry(ctx context.Context, userID, taskID, projectID int64, i int, rawURL string, durationMs int64) (string, int64, string, error) {
	stored, id, mime, err := s.persistSlot(ctx, userID, taskID, projectID, i, rawURL, durationMs)
	if err == nil || !isRemoteURL(rawURL) || !isRetryablePersistError(err) {
		return stored, id, mime, err
	}
	for n, d := range persistRetryDelays {
		if werr := waitWithContext(ctx, d); werr != nil {
			return "", 0, "", err
		}
		log.Printf("generation: persist output retry %d/%d task=%d idx=%d after: %v", n+1, len(persistRetryDelays), taskID, i, err)
		stored, id, mime, err = s.persistSlot(ctx, userID, taskID, projectID, i, rawURL, durationMs)
		if err == nil {
			return stored, id, mime, nil
		}
		if !isRetryablePersistError(err) {
			return "", 0, "", err
		}
	}
	return "", 0, "", err
}
