package library

import (
	"context"
	"log"
	"time"
)

// DefaultGracePeriod 是孤儿对象的宽限期：删除后 7 天内撤销、重新引用都来得及。
const DefaultGracePeriod = 7 * 24 * time.Hour

type objectDeleter interface {
	DeleteObject(ctx context.Context, key string) error
}

// SweepResult 是一轮清扫的统计，字段与 POST /admin/gc 的响应一一对应。
type SweepResult struct {
	Deleted         int
	Skipped         int
	TotalCandidates int
}

// OrphanSweeper 真正删除过了宽限期的孤儿对象。桌面端没有运维，由 sidecar 启动时跑一次、
// 之后每天跑一次（Start）；POST /admin/gc 也可手动触发。
type OrphanSweeper struct {
	orphans OrphansRepository
	storage objectDeleter
	db      Querier
}

func NewOrphanSweeper(orphans OrphansRepository, storage objectDeleter, db Querier) *OrphanSweeper {
	return &OrphanSweeper{orphans: orphans, storage: storage, db: db}
}

// Candidates 只列出早于宽限期的孤儿，不碰文件（/admin/gc?dryRun=1 用）。
func (s *OrphanSweeper) Candidates(grace time.Duration) ([]OrphanObject, error) {
	return s.orphans.ListReady(grace)
}

// Sweep 扫描早于宽限期的孤儿，逐个用 IsStillReferenced 复核（宽限期内可能被重新引用），
// 仍无引用才删文件 + 删孤儿行。不调整配额——标记孤儿时调用方已经扣过。
// 删文件失败（如 Windows 上文件正被占用）只计 skipped、保留孤儿行，下一轮重试。
func (s *OrphanSweeper) Sweep(ctx context.Context, grace time.Duration) (SweepResult, error) {
	list, err := s.orphans.ListReady(grace)
	if err != nil {
		return SweepResult{}, err
	}
	res := SweepResult{TotalCandidates: len(list)}
	for _, o := range list {
		if ctx.Err() != nil {
			break
		}
		referenced, err := IsStillReferenced(s.db, o.ObjectKey)
		if err != nil {
			log.Printf("gc: refcheck %s failed: %v", o.ObjectKey, err)
			res.Skipped++
			continue
		}
		if referenced {
			// 在宽限窗口内被重新引用——去掉孤儿标记。
			_ = s.orphans.Delete(o.ObjectKey)
			res.Skipped++
			continue
		}
		if err := s.storage.DeleteObject(ctx, o.ObjectKey); err != nil {
			log.Printf("gc: DeleteObject %s failed: %v", o.ObjectKey, err)
			res.Skipped++
			continue
		}
		if err := s.orphans.Delete(o.ObjectKey); err != nil {
			log.Printf("gc: orphan row delete %s failed: %v", o.ObjectKey, err)
		}
		res.Deleted++
	}
	return res, nil
}

// Start 在后台定时清扫：立即跑一次（应用常常只开几个小时，等满一天才跑等于不跑），之后每 interval 一次。
// interval<=0 回落到 24 小时。返回的通道在 ctx 取消、goroutine 退出后关闭，main 据此等它停下再关库。
func (s *OrphanSweeper) Start(ctx context.Context, interval, grace time.Duration) <-chan struct{} {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			res, err := s.Sweep(ctx, grace)
			if err != nil {
				log.Printf("gc: sweep failed: %v", err)
			} else if res.TotalCandidates > 0 {
				log.Printf("gc: deleted=%d skipped=%d candidates=%d", res.Deleted, res.Skipped, res.TotalCandidates)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}
