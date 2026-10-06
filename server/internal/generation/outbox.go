package generation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"time"
)

// 结果投递外发箱(v5 §5.6,保证 6)。
//
// 任务终态与「要通知发起方」在同一个事务里落库(步骤 15 的 finishTx 插行),独立 worker
// 带 fencing 租约重试投递——结果不再因崩溃或部署丢失。投递的业务写入(handler)已经
// 全部幂等且如实返错(§5.7),重投安全。
//
// fencing:delivery_version 每次领取 +1,完成/失败写回必须带对它——租约过期被接管后,
// 旧 worker 对本行的写全部落空。代次取自领取语句的 `RETURNING delivery_version`。

// outboxRow 是一行待投递记录。
type outboxRow struct {
	ID              int64
	TaskID          int64
	Sink            json.RawMessage
	Payload         json.RawMessage
	Attempt         int
	DeliveryVersion int64
}

// outboxPayload 是 payload 列的 JSON 形状——SinkResult 的持久化形态。
type outboxPayload struct {
	ModelID      string   `json:"modelId,omitempty"`
	OutputURLs   []string `json:"outputUrls,omitempty"`
	UpstreamURLs []string `json:"upstreamUrls,omitempty"`
	AssetIDs     []int64  `json:"assetIds,omitempty"`
	Text         string   `json:"text,omitempty"`
	Err          string   `json:"err,omitempty"`
}

const (
	outboxLeaseSec   = 300 // 投递租约:worker 崩溃后过期可被接管
	outboxMaxAttempt = 8   // 超上限置 dead(告警,人工查因修复后 SQL 置回 pending)
)

// InsertOutboxTx 在调用方事务里插一行外发箱(步骤 15 的终态事务用)。
// uk_outbox_task 撞键=协议外状态(终态被提交了两次):让整个终态事务回滚(安全失败),
// 调用方必须告警,不能当瞬时错误无限重试。sink 为空的老任务由调用方跳过,不插。
func (r *SQLiteRepository) InsertOutboxTx(tx *sql.Tx, taskID int64, sink json.RawMessage, res SinkResult) error {
	payload, err := json.Marshal(outboxPayload{
		ModelID:      res.ModelID,
		OutputURLs:   res.OutputURLs,
		UpstreamURLs: res.UpstreamURLs,
		AssetIDs:     res.AssetIDs,
		Text:         res.Text,
		Err:          res.Err,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO generation_sink_outbox (task_id, sink, payload) VALUES (?, ?, ?)`,
		taskID, string(sink), string(payload))
	return err
}

// claimOutboxDue 领取一批可投递的行:pending 到点、或 delivering 但租约过期(接管)。
// 两步走:先扫候选 id,再逐行 CAS 领取(delivery_version+1)——单行 UPDATE 保证并发
// worker 恰好一个领到。返回已领到的行(带新代次)。
func (r *SQLiteRepository) claimOutboxDue(limit int) ([]outboxRow, error) {
	rows, err := r.db.Query(`SELECT id FROM generation_sink_outbox
		WHERE (state='pending' AND (next_retry_at IS NULL OR next_retry_at <= CURRENT_TIMESTAMP))
		   OR (state='delivering' AND locked_until < CURRENT_TIMESTAMP)
		ORDER BY id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]outboxRow, 0, len(ids))
	for _, id := range ids {
		var version int64
		err := r.db.QueryRow(`UPDATE generation_sink_outbox
			SET state='delivering', locked_until = datetime('now', ?),
			    attempt = attempt + 1, delivery_version = delivery_version + 1
			WHERE id = ?
			  AND ((state='pending' AND (next_retry_at IS NULL OR next_retry_at <= CURRENT_TIMESTAMP))
			    OR (state='delivering' AND locked_until < CURRENT_TIMESTAMP))
			RETURNING delivery_version`, sqlSecondsOffset(outboxLeaseSec), id).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			continue // 别的 worker 先领走了
		}
		if err != nil {
			return nil, err
		}
		var row outboxRow
		var sink, payload []byte
		if err := r.db.QueryRow(`SELECT id, task_id, sink, payload, attempt FROM generation_sink_outbox WHERE id=?`, id).
			Scan(&row.ID, &row.TaskID, &sink, &payload, &row.Attempt); err != nil {
			continue
		}
		row.Sink = sink
		row.Payload = payload
		row.DeliveryVersion = version
		out = append(out, row)
	}
	return out, nil
}

// completeOutbox 投递成功写回 done。带代次:失租的旧 worker 写不进(影响 0 行)。
func (r *SQLiteRepository) completeOutbox(id, version int64) (bool, error) {
	res, err := r.db.Exec(`UPDATE generation_sink_outbox
		SET state='done', locked_until=NULL
		WHERE id=? AND state='delivering' AND delivery_version=?`, id, version)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// failOutbox 投递失败:未超上限 → 置回 pending 按指数退避排下次;超上限 → dead 告警。
// 同样带代次 fencing。
func (r *SQLiteRepository) failOutbox(id, version int64, attempt int, errMsg string) (bool, error) {
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	if attempt >= outboxMaxAttempt {
		res, err := r.db.Exec(`UPDATE generation_sink_outbox
			SET state='dead', locked_until=NULL, last_error=?
			WHERE id=? AND state='delivering' AND delivery_version=?`, errMsg, id, version)
		if err != nil {
			return false, err
		}
		n, err := res.RowsAffected()
		if n == 1 {
			log.Printf("[gen-outbox] 告警: 行 %d 投递超上限置 dead: %s (人工修复后 UPDATE state='pending' 重放)", id, errMsg)
		}
		return n == 1, err
	}
	// 指数退避:2^attempt 秒,封顶 10 分钟。
	backoff := 1 << attempt
	if backoff > 600 {
		backoff = 600
	}
	res, err := r.db.Exec(`UPDATE generation_sink_outbox
		SET state='pending', locked_until=NULL, next_retry_at = datetime('now', ?), last_error=?
		WHERE id=? AND state='delivering' AND delivery_version=?`, sqlSecondsOffset(backoff), errMsg, id, version)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RunOutboxWorker 每实例常驻:周期领取并投递。多实例安全靠领取 CAS + delivery_version。
func (s *Service) RunOutboxWorker(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.pumpOutboxOnce(ctx)
		}
	}
}

// pumpOutboxOnce 投递一批到期行。ctx 取消后不再开始新的行，在途行用同一个 ctx 投递；
// 被取消打断的行不记失败（不消耗重试次数），租约过期后下次启动接管重投。
func (s *Service) pumpOutboxOnce(ctx context.Context) {
	repo, ok := s.repo.(*SQLiteRepository)
	if !ok {
		return // 测试 fake 环境无 outbox
	}
	batch, err := repo.claimOutboxDue(50)
	if err != nil {
		log.Printf("[gen-outbox] 领取失败: %v", err)
		return
	}
	for _, row := range batch {
		if ctx.Err() != nil {
			return
		}
		var p outboxPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			// payload 损坏是持续性故障:直接按失败路径走向 dead,不无限重试。
			_, _ = repo.failOutbox(row.ID, row.DeliveryVersion, outboxMaxAttempt, "payload 损坏: "+err.Error())
			continue
		}
		// M3：成功结果投递前用任务当前 output 覆盖产物地址。首投失败退避期间用户可能已手动
		// 「保存到本地」（RepersistTask 改写了 output），payload 里还是收口时的上游外链。
		if p.Err == "" && len(p.OutputURLs) > 0 {
			if urls, ids, ok := repo.currentTaskOutputs(row.TaskID); ok && len(urls) == len(p.OutputURLs) {
				p.OutputURLs, p.AssetIDs = urls, ids
			}
		}
		err := dispatchSink(ctx, row.Sink, SinkResult{
			TaskID:       row.TaskID,
			ModelID:      p.ModelID,
			OutputURLs:   p.OutputURLs,
			UpstreamURLs: p.UpstreamURLs,
			AssetIDs:     p.AssetIDs,
			Text:         p.Text,
			Err:          p.Err,
		})
		if err != nil && ctx.Err() != nil {
			log.Printf("[gen-outbox] 行 %d 投递被关闭打断，留待下次启动接管: %v", row.ID, err)
			return
		}
		if err != nil {
			log.Printf("[gen-outbox] 行 %d task=%d 投递失败(第 %d 次): %v", row.ID, row.TaskID, row.Attempt, err)
			_, _ = repo.failOutbox(row.ID, row.DeliveryVersion, row.Attempt, err.Error())
			continue
		}
		if won, err := repo.completeOutbox(row.ID, row.DeliveryVersion); err != nil {
			log.Printf("[gen-outbox] 行 %d 写回 done 失败: %v", row.ID, err)
		} else if !won {
			// 失租:租约过期被接管,新 worker 会重投——handler 幂等,重复投递无害。
			log.Printf("[gen-outbox] 行 %d 失租(代次已被接管),放弃写回", row.ID)
		}
	}
}

// currentTaskOutputs 读成功任务当前 output 里的 outputUrls / assetIds。读不到或不是成功态时 ok=false，
// 调用方沿用 payload 原值。
func (r *SQLiteRepository) currentTaskOutputs(taskID int64) ([]string, []int64, bool) {
	var raw sql.NullString
	if err := r.db.QueryRow(`SELECT output FROM generation_tasks WHERE id=? AND status='succeeded'`, taskID).Scan(&raw); err != nil || !raw.Valid {
		return nil, nil, false
	}
	var out struct {
		OutputURLs []string `json:"outputUrls"`
		AssetIDs   []int64  `json:"assetIds"`
	}
	if err := json.Unmarshal([]byte(raw.String), &out); err != nil || len(out.OutputURLs) == 0 {
		return nil, nil, false
	}
	return out.OutputURLs, out.AssetIDs, true
}
