package http

import (
	"context"
	"database/sql"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/projects"
)

// ProjectCleaner 后台异步清理已软删的画布项目。删项目时只软删(秒回)，这里把原来同步做的
// 「列资产 → 删 asset 行 → 标孤儿给 GC → 删生成任务 → 回收配额 → 硬删项目」慢慢清。
// 画布项目内生成/上传的文件可能很多，同步删会卡请求/超时；软删 + worker 可重入抗重启。
type ProjectCleaner struct {
	repo        projects.Repository
	assetRepo   assets.Repository
	users       storageUserRepo
	taskCleanup taskProjectCleanup
	orphans     library.OrphansRepository
	db          *sql.DB
}

func NewProjectCleaner(repo projects.Repository, assetRepo assets.Repository, users storageUserRepo, taskCleanup taskProjectCleanup, orphans library.OrphansRepository, db *sql.DB) *ProjectCleaner {
	return &ProjectCleaner{repo: repo, assetRepo: assetRepo, users: users, taskCleanup: taskCleanup, orphans: orphans, db: db}
}

// Start 启动后台清理：先立即跑一次(抗重启续删)，之后每 interval 一拍。
func (c *ProjectCleaner) Start(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			_ = c.purgePending(10)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (c *ProjectCleaner) purgePending(limit int) error {
	pend, err := c.repo.ListPendingDeletion(limit)
	if err != nil {
		return err
	}
	for _, p := range pend {
		c.purge(p.UserID, p.ID)
	}
	return nil
}

// purge 清一个软删项目（复刻原 handler.Delete 的清理逻辑，末尾改硬删项目行）。
func (c *ProjectCleaner) purge(userID, projectID int64) {
	// 先快照 asset row，删行后还能按 object_key 跑 tryReclaim（标孤儿给 GC，sweeper 删前复核）。
	var assetList []assets.AssetRecord
	if c.assetRepo != nil {
		assetList, _ = c.assetRepo.ListForProject(projectID)
	}

	tx, err := c.db.Begin()
	if err != nil {
		return
	}
	if _, err := tx.Exec(`DELETE FROM assets WHERE project_id=?`, projectID); err != nil {
		_ = tx.Rollback()
		return
	}
	for _, a := range assetList {
		if err := library.TryReclaim(library.ReclaimDeps{Querier: tx, Execer: tx, Orphans: c.orphans}, a.ObjectKey, userID, a.SizeBytes); err != nil {
			_ = tx.Rollback()
			return
		}
	}
	if err := tx.Commit(); err != nil {
		return
	}

	if c.taskCleanup != nil {
		_ = c.taskCleanup.DeleteForProject(projectID)
	}

	// 只为不再被任何地方引用的 object_key 回收配额（引用 row 的 size_bytes=0 会跳过）。
	if c.users != nil {
		var freed int64
		for _, a := range assetList {
			if a.SizeBytes == 0 {
				continue
			}
			if ref, err := library.IsStillReferenced(c.db, a.ObjectKey); err == nil && !ref {
				freed += a.SizeBytes
			}
		}
		if freed > 0 {
			_ = c.users.AddStorage(userID, -freed)
		}
	}

	_ = c.repo.HardDelete(projectID) // workflow_snapshots 经 FK ON DELETE CASCADE 自动删
}
