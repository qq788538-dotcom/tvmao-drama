package generation

// QueueModelStat 是某模型当前媒体生成的队列态(全局,跨实例——直接从 generation_tasks 聚合)。
//
// 口径不能只看 status:同步生图(一次 HTTP 拿结果)全程停在 status=queued,从不经过
// processing,只在成功时直接跳 succeeded。按 status 分类会把「正在打上游的同步任务」
// 算成排队——上限 20 却显示排队 1、在跑 0,数字自相矛盾。
//
// 真正区分二者的是 claim:任务拿到并发名额、开始打上游时会写 owner_instance +
// heartbeat_at(统一接管器靠心跳超时识别孤儿)。
// 所以 已 claim=在跑,queued 且未 claim=真的在等名额。
//
// 持久化队列上线后又多了 running 状态(worker 领取后置)。它必须一起纳入统计,
// 否则队列里正在跑的任务会从监控上凭空消失——上线当晚就踩到了:两条任务明明
// 已被领取执行,页面却显示「在跑 0」。新增状态时别忘了这里。
type QueueModelStat struct {
	ModelID      string `json:"modelId"`
	Queued       int    `json:"queued"`       // 排队等名额
	Processing   int    `json:"processing"`   // 在跑(打上游中)
	RetryPending int    `json:"retryPending"` // 上游权威失败后等待到点重试(v5 §4.2,复用同一笔冻结)
	Concurrency  int    `json:"concurrency"`  // 该模型并发上限(gen_control.concurrency,默认 200)
}

// QueueStats 返回所有"有在途任务"的模型的队列态,按 processing+queued 多的排前。
// 覆盖全部媒体生成入口(画布/候选图/材料视频/去字幕/步骤4/资产/agent)——它们都落 generation_tasks。
func (s *Service) QueueStats() ([]QueueModelStat, error) {
	// retry_pending 必须纳入(v5 §4.2)：重试中的任务持有冻结、仍是在途——漏掉它就是
	// 8e8d292 修过的「在跑任务从页面消失」的同类回归。
	rows, err := s.db.Query(`
		SELECT model_id,
		       SUM(CASE WHEN status IN ('running','processing') OR (status='queued' AND owner_instance IS NOT NULL) THEN 1 ELSE 0 END) AS running,
		       SUM(CASE WHEN status = 'queued' AND owner_instance IS NULL THEN 1 ELSE 0 END) AS waiting,
		       SUM(CASE WHEN status = 'retry_pending' THEN 1 ELSE 0 END) AS retrying
		  FROM generation_tasks
		 WHERE status IN ('queued','running','processing','retry_pending')
		 GROUP BY model_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byModel := map[string]*QueueModelStat{}
	for rows.Next() {
		var modelID string
		var running, waiting, retrying int
		if err := rows.Scan(&modelID, &running, &waiting, &retrying); err != nil {
			return nil, err
		}
		byModel[modelID] = &QueueModelStat{ModelID: modelID, Processing: running, Queued: waiting, RetryPending: retrying}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]QueueModelStat, 0, len(byModel))
	for _, st := range byModel {
		// 并发上限按模型配置补充(缺失取默认 200)。取不到模型 → 留 0。
		if m, err := s.models.GetByModelID(st.ModelID); err == nil && m != nil {
			st.Concurrency = m.GenConcurrency()
		}
		out = append(out, *st)
	}
	// 在途多的排前(processing 优先,其次 queued+retry)。
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			a, b := out[i], out[j]
			if b.Processing+b.Queued+b.RetryPending > a.Processing+a.Queued+a.RetryPending {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}
