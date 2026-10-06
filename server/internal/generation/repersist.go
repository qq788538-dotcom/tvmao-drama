package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// RunRepersistWorker 补搬「结果里留着上游 http 外链」的成功任务:persistOutputs 首搬
// 失败(下载超时/上游瞬时 4xx/对象存储抖动)时静默保留上游 URL、任务照常成功;上游中转
// (Cloudflare R2 之类)大约两周清文件,不补搬就永久裂图。线上 2026-08-29 盘点 87 条,
// 48 条已 404——那批只能让用户重生成,这个 worker 保证以后不再出现第二批。
//
// 目标识别不加新列、不打标记:`status='succeeded' 且 outputUrls[0] 以 http 开头` 本身
// 就是「泄漏」的定义,再加一列就是第二处要保持一致的状态。重试计数 persistAttempts 与
// 下次可试时间 persistNextAt 记在 output JSON 里,间隔翻倍(5m→10m→…→约 21h),满
// RepersistMaxAttempts 次不再碰——10 次总窗口约 1.8 天,覆盖「上游整晚很慢」而不至于
// 对早已清掉的文件永远打 404。任何失败都计一次,不区分 404:上游有「先返回 URL、文件
// 几分钟后才真正就绪」的假 404。
//
// 挂在 leader 单例组(main.go SuperviseLeader):蓝绿双活期只跑一份。串行处理、每轮限
// 量,不与在跑的生成任务抢带宽。
func (s *Service) RunRepersistWorker(ctx context.Context, store RepersistStore, rewriters ...MediaRefRewriter) {
	t := time.NewTicker(repersistInterval)
	defer t.Stop()
	for {
		s.RepersistOnce(ctx, store, rewriters...)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

const (
	repersistInterval    = 5 * time.Minute
	repersistBatch       = 50
	repersistWindowDays  = 30
	RepersistMaxAttempts = 10
	repersistBaseDelay   = 5 * time.Minute
)

// RepersistStore 是 Repository 之外的窄接口(不并进 Repository:那会牵动所有测试
// fake),SQLiteRepository 实现它,main.go 显式传入。
type RepersistStore interface {
	// ListExternalOutputs 找 status='succeeded'、outputUrls[0] 以 http 开头、created_at
	// 在 sinceDays 内、persistAttempts < maxAttempts、persistNextAt 为空或已到期的任务,
	// id 倒序(新的先救——它们离上游清理期最远、也最可能还有人在看)。
	ListExternalOutputs(sinceDays, maxAttempts, limit int, now time.Time) ([]Task, error)
	// UpdateOutput 整体替换 output JSON;outputKind 非空时同步 output_kind 列。
	UpdateOutput(taskID int64, output json.RawMessage, outputKind string) error
	// MarkRepersistAttempt 把 persistAttempts / persistNextAt 写进 output JSON。
	MarkRepersistAttempt(taskID int64, attempts int, nextAt time.Time) error
}

// MediaRefRewriter 把某条媒体引用的所有副本从旧 URL 改成新 URL。补搬后的 URL 不只在
// generation_tasks.output 里:drama 资产表直接存了它(image_object_key / candidates),
// 画布快照的节点 content / history 也存了它。各持有方自己知道自己存在哪、怎么改、
// 改完要不要广播,generation 只负责喊一声——不在这里 import drama / canvasgen 反向依赖。
type MediaRefRewriter interface {
	RewriteMediaRef(ctx context.Context, userID int64, oldURL, newURL string) error
}

// RepersistOnce 跑一轮,返回(修复, 放弃, 失败)条数;供 worker 与测试调用。
func (s *Service) RepersistOnce(ctx context.Context, store RepersistStore, rewriters ...MediaRefRewriter) (fixed, gaveUp, failed int) {
	if s.storage == nil || store == nil {
		return 0, 0, 0
	}
	now := time.Now()
	tasks, err := store.ListExternalOutputs(repersistWindowDays, RepersistMaxAttempts, repersistBatch, now)
	if err != nil {
		log.Printf("repersist: list failed: %v", err)
		return 0, 0, 0
	}
	for _, t := range tasks {
		if ctx.Err() != nil {
			return
		}
		if err := s.repersistTask(ctx, store, t, rewriters); err != nil {
			attempts := repersistAttempts(t.Output) + 1
			nextAt := now.Add(repersistDelay(attempts))
			if attempts >= RepersistMaxAttempts {
				gaveUp++
				log.Printf("repersist: task=%d 第 %d 次失败,放弃: %v", t.ID, attempts, err)
			} else {
				failed++
				log.Printf("repersist: task=%d 第 %d 次失败,%s 后再试: %v", t.ID, attempts, repersistDelay(attempts), err)
			}
			if merr := store.MarkRepersistAttempt(t.ID, attempts, nextAt); merr != nil {
				log.Printf("repersist: task=%d 记录重试次数失败: %v", t.ID, merr)
			}
			continue
		}
		fixed++
	}
	return fixed, gaveUp, failed
}

// repersistDelay 第 attempt 次失败后等多久:5m × 2^(attempt-1)。attempt 从 1 起。
func repersistDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return repersistBaseDelay << uint(attempt-1)
}

func repersistAttempts(output json.RawMessage) int {
	var m struct {
		Attempts int `json:"persistAttempts"`
	}
	_ = json.Unmarshal(output, &m)
	return m.Attempts
}

// repersistTask 补搬一条任务的所有外链槽位,再同形回写。任一槽位失败整条算失败
// (已搬成功的槽位对象与 asset 行都留着,下次重试 persistSlot 走 FindByTaskSlot 直接
// 复用,不重复下载)。
func (s *Service) repersistTask(ctx context.Context, store RepersistStore, t Task, rewriters []MediaRefRewriter) error {
	var output map[string]any
	if err := json.Unmarshal(t.Output, &output); err != nil {
		return fmt.Errorf("output 不是合法 JSON: %w", err)
	}
	urls := anyStrings(output["outputUrls"])
	assetIDs := anyInt64s(output["assetIds"], len(urls))
	pid := int64(0)
	if t.ProjectID != nil {
		pid = *t.ProjectID
	}
	durMs := int64(0)
	if len(t.Input) > 0 {
		var in struct {
			Inputs map[string]any `json:"inputs"`
		}
		if json.Unmarshal(t.Input, &in) == nil {
			durMs = durationMsFromInputs(in.Inputs)
		}
	}

	replacements := map[string]string{}
	outputKind := ""
	for i, raw := range urls {
		if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
			continue
		}
		newURL, assetID, mime, err := s.repersistSlot(ctx, t.UserID, t.ID, pid, i, raw, durMs)
		if err != nil {
			return fmt.Errorf("slot=%d: %w", i, err)
		}
		urls[i] = newURL
		assetIDs[i] = assetID
		if i == 0 {
			outputKind = outputKindFromMime(mime)
		}
		replacements[raw] = newURL
	}
	if len(replacements) == 0 {
		return errors.New("没有可搬的槽位")
	}

	output["outputUrls"] = urls
	output["assetIds"] = assetIDs
	if outputKind != "" {
		output["outputKind"] = outputKind
	}
	// 修好了,重试簿记就没意义了——留着会让读 output 的人误以为还在重试。
	delete(output, "persistAttempts")
	delete(output, "persistNextAt")
	next, err := json.Marshal(output)
	if err != nil {
		return err
	}
	if err := store.UpdateOutput(t.ID, next, outputKind); err != nil {
		return fmt.Errorf("回写 output: %w", err)
	}
	for oldURL, newURL := range replacements {
		for _, rw := range rewriters {
			if err := rw.RewriteMediaRef(ctx, t.UserID, oldURL, newURL); err != nil {
				// output 已经改好,这里失败不回滚:下一轮 ListExternalOutputs 不会再选中
				// 这条(outputUrls 已是 media://),所以必须记日志让人能手工补——比起
				// 回滚 output 再整条重下载,留着已修好的部分损失更小。
				log.Printf("repersist: task=%d 副本改写失败 %T old=%s: %v", t.ID, rw, oldURL, err)
			}
		}
		log.Printf("repersist: task=%d 已搬运 %s → %s", t.ID, oldURL, newURL)
	}
	return nil
}

// repersistSlot 先查槽位复用(上一轮可能对象已传成功、只是后面的回写失败),再走与
// persistOutputs 完全相同的 persistSlot。
func (s *Service) repersistSlot(ctx context.Context, userID, taskID, projectID int64, i int, raw string, durMs int64) (string, int64, string, error) {
	if s.assets != nil {
		if existing, err := s.assets.FindByTaskSlot(taskID, i); err == nil && existing != nil {
			return existing.URL, existing.ID, existing.MimeType, nil
		}
	}
	return s.persistSlot(ctx, userID, taskID, projectID, i, raw, durMs)
}

func anyStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func anyInt64s(v any, n int) []int64 {
	out := make([]int64, n)
	arr, _ := v.([]any)
	for i := 0; i < n && i < len(arr); i++ {
		switch x := arr[i].(type) {
		case float64:
			out[i] = int64(x)
		case json.Number:
			f, _ := x.Int64()
			out[i] = f
		}
	}
	return out
}
