package http

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/projects"
)

// ProjectCleaner 后台异步清理已软删的画布项目。删项目时只软删(秒回)，这里把
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

// Start 启动后台清理：先立即跑一次(抗重启续删)，之后每 interval 一拍（<=0 回落到 30 秒）。
// 返回的通道在 ctx 取消、goroutine 退出后关闭，main 据此等它停下再关库。
func (c *ProjectCleaner) Start(ctx context.Context, interval time.Duration) <-chan struct{} {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			if err := c.purgePending(10); err != nil {
				log.Printf("project cleanup: 列出待清理项目失败: %v", err)
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

func (c *ProjectCleaner) purgePending(limit int) error {
	pend, err := c.repo.ListPendingDeletion(limit)
	if err != nil {
		return err
	}
	for _, p := range pend {
		if err := c.purge(p.UserID, p.ID); err != nil {
			log.Printf("project cleanup: 清理项目 %d 失败（下一轮重试）: %v", p.ID, err)
		}
	}
	return nil
}

// purge 清一个软删项目（复刻原 handler.Delete 的清理逻辑，末尾改硬删项目行）。
// 事务内的步骤失败会整体回滚并返回错误，下一轮重试；提交之后的收尾步骤失败只记日志，
// 因为它们要么幂等（HardDelete 下一轮再来），要么只影响用量计数。
func (c *ProjectCleaner) purge(userID, projectID int64) error {
	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	// 在事务内快照 asset 行（在事务外列的话，两步之间插入的行会被删掉却不标孤儿，文件永久泄漏）。
	// 删行后还要按 object_key 跑 TryReclaim（标孤儿给 GC，sweeper 删前复核）。
	assetList, err := listProjectAssetsTx(tx, projectID)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("list assets: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM assets WHERE project_id=?`, projectID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete assets: %w", err)
	}
	for _, a := range assetList {
		if err := library.TryReclaim(library.ReclaimDeps{Querier: tx, Execer: tx, Orphans: c.orphans}, a.ObjectKey, userID, a.SizeBytes); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("reclaim %s: %w", a.ObjectKey, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if c.taskCleanup != nil {
		if err := c.taskCleanup.DeleteForProject(projectID); err != nil {
			log.Printf("project cleanup: 删除项目 %d 的生成任务失败: %v", projectID, err)
		}
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
			if err := c.users.AddStorage(userID, -freed); err != nil {
				log.Printf("project cleanup: 回收项目 %d 的用量 %d 字节失败: %v", projectID, freed, err)
			}
		}
	}

	// workflow_snapshots 经 FK ON DELETE CASCADE 自动删
	if err := c.repo.HardDelete(projectID); err != nil {
		return fmt.Errorf("hard delete: %w", err)
	}
	return nil
}

// listProjectAssetsTx 在事务内列出项目的 asset 行，只取清理需要的 object_key 和 size_bytes。
func listProjectAssetsTx(tx *sql.Tx, projectID int64) ([]assets.AssetRecord, error) {
	rows, err := tx.Query(`SELECT object_key, size_bytes FROM assets WHERE project_id=?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []assets.AssetRecord
	for rows.Next() {
		var a assets.AssetRecord
		if err := rows.Scan(&a.ObjectKey, &a.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
