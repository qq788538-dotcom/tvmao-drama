package generation

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
)

// persistSlot 写完对象之后的「asset 行 + 存储用量」事务。
//
// 两件事在这里收口：
//   - P26：事务偶发失败（写锁争用等）以前只打日志、照常返回成功，对象既不计配额、删任务
//     也回收不到、节点还缺 assetId。现在先有界重试，仍失败再按产物形态分流（见 persistSlot）。
//   - P9：项目在生成期间被软删（或已被 ProjectCleaner 硬删）时不插资产行、不计配额，
//     对象在同一事务里登记孤儿交给 GC。检查放在事务里是兜底：提交入口的项目校验挡不住
//     「先校验后删除」的竞态。

// errProjectDeleted：产物所属项目已删除。确定性的，不重试；finalize 据此失败退款。
var errProjectDeleted = errors.New("项目已删除")

// projectDeletedReason 是项目删除导致任务终止时写给用户看的原因。
const projectDeletedReason = "项目已删除，任务已取消并退款"

// errSlotAssetRecord：资产事务重试耗尽。http(s) 产物据此保留上游地址（可手动补搬）。
var errSlotAssetRecord = errors.New("产物已保存但登记失败")

// assetTxRetryDelays 是资产事务失败后的重试间隔，次数即重试上限。包级变量：测试置零。
var assetTxRetryDelays = []time.Duration{300 * time.Millisecond, time.Second}

// recordSlotAsset 写入槽位 asset 行并累计存储用量（同一事务），失败有界重试。
// 返回 asset id；项目已删除时返回 errProjectDeleted（对象已登记孤儿）。
func (s *Service) recordSlotAsset(ctx context.Context, rec assets.AssetRecord, outputIndex int) (int64, error) {
	id, err := s.recordSlotAssetOnce(ctx, rec, outputIndex)
	for n := 0; err != nil && !errors.Is(err, errProjectDeleted) && n < len(assetTxRetryDelays); n++ {
		if werr := waitWithContext(ctx, assetTxRetryDelays[n]); werr != nil {
			return 0, err
		}
		log.Printf("generation: persist asset slot retry %d/%d task=%d idx=%d after: %v", n+1, len(assetTxRetryDelays), rec.SourceTaskID, outputIndex, err)
		id, err = s.recordSlotAssetOnce(ctx, rec, outputIndex)
	}
	return id, err
}

func (s *Service) recordSlotAssetOnce(ctx context.Context, rec assets.AssetRecord, outputIndex int) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if rec.ProjectID != 0 {
		alive, err := projectAliveTx(ctx, tx, rec.ProjectID)
		if err != nil {
			return 0, err
		}
		if !alive {
			// 不插行、不计量；对象无人引用，登记孤儿（7 天宽限后由 GC 删除）。
			if s.reclaimer != nil {
				if _, err := s.reclaimer.ReclaimTx(tx, rec.ObjectKey, rec.UserID, rec.SizeBytes); err != nil {
					return 0, err
				}
			}
			if err := tx.Commit(); err != nil {
				return 0, err
			}
			return 0, errProjectDeleted
		}
	}
	// asset 行与存储用量同一事务:Insert 成功 AddStorage 失败的
	// 「永久少计存储」窗口随事务消失;唯一键兜底并发重放,只有真正新插入才计量。
	id, inserted, err := s.assets.InsertSlotTx(tx, rec, outputIndex)
	if err != nil {
		return 0, err
	}
	if inserted && s.users != nil && rec.SizeBytes > 0 {
		if err := s.users.AddStorageTx(tx, rec.UserID, rec.SizeBytes); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// projectAliveTx：项目存在且未软删。硬删（ProjectCleaner 清理完）后行不存在，同样视为已删除。
func projectAliveTx(ctx context.Context, tx *sql.Tx, projectID int64) (bool, error) {
	var alive bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id = ? AND deleted_at IS NULL)`, projectID).Scan(&alive)
	return alive, err
}

// markSlotOrphan 把没登记上 asset 行的对象登记孤儿（尽力而为：资产事务刚失败过，这一步也
// 可能失败，只记日志）。不随落盘 ctx 取消——失租/超时后对象照样要有人回收。
func (s *Service) markSlotOrphan(ctx context.Context, key string, userID, size int64) {
	if s.reclaimer == nil || s.db == nil {
		return
	}
	octx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishAttemptTimeout)
	defer cancel()
	err := func() error {
		tx, err := s.db.BeginTx(octx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := s.reclaimer.ReclaimTx(tx, key, userID, size); err != nil {
			return err
		}
		return tx.Commit()
	}()
	if err != nil {
		log.Printf("generation: 登记孤儿对象失败 key=%s: %v", key, err)
	}
}
