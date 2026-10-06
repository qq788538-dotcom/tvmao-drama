package workflows

import (
	"context"
	"log"
)

type objectDeleter interface {
	DeleteObject(ctx context.Context, key string) error
}

// DeleteNowQueue 是桌面端的 cleanupQueue。参考仓把失败复制留下的对象写进 cleanup_jobs 表，由
// worker 异步重试删除（对象存储的删除可能瞬时失败）；本地删文件是同步、几乎不失败的系统调用，
// 所以直接删。删不掉只记日志：这个对象没有任何 asset 行引用，孤儿清扫也看不到它，只能靠日志排查
// （见计划“风险”一节）。
type DeleteNowQueue struct {
	storage objectDeleter
}

func NewDeleteNowQueue(storage objectDeleter) *DeleteNowQueue {
	return &DeleteNowQueue{storage: storage}
}

// EnqueueObjectKey 立即删除 key 对应的文件；永远返回 nil——走到这里时调用方的主操作已经失败，
// 不该再用回滚的错误覆盖它（与参考仓 CleanupRepo.EnqueueObjectKey 吞错的口径一致）。
func (q *DeleteNowQueue) EnqueueObjectKey(key string) error {
	if q == nil || q.storage == nil || key == "" {
		return nil
	}
	if err := q.storage.DeleteObject(context.Background(), key); err != nil {
		log.Printf("duplicate rollback: DeleteObject %q: %v", key, err)
	}
	return nil
}
