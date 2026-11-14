package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 手动「保存到本地」：产物首搬重试 3 次仍失败时，任务照常成功、output 里保留上游
// 外链（assetIds 对应位置为 0），画布节点显示「未保存到本地」和重试按钮；用户点重试走这里。
// 不做后台定时自动补搬：行为是「重试 3 次后提示并可手动重试」。补搬与首搬共用 persistSlot，key / URL / asset 行逐字同形。

// ErrNothingToRepersist：任务不是成功态，或所有产物都已在本地。HTTP 层映射 409。
var ErrNothingToRepersist = errors.New("该任务没有需要保存到本地的产物")

// RepersistStore 是补搬需要的窄写接口（SQLiteRepository 实现）。
type RepersistStore interface {
	// UpdateOutput 整体替换 output JSON；outputKind 非空时同步 output_kind 列。
	UpdateOutput(taskID int64, output json.RawMessage, outputKind string) error
}

// RepersistReplacement 是一个被搬回本地的槽位。
type RepersistReplacement struct {
	Index   int
	OldURL  string
	NewURL  string
	AssetID int64
}

// RepersistResult 交给 HTTP 层：Sink 用于回写画布节点，OutputURLs/AssetIDs 是回写后的整组产物。
type RepersistResult struct {
	Sink         json.RawMessage
	OutputURLs   []string
	AssetIDs     []int64
	Replacements []RepersistReplacement
}

func (s *Service) repersistStore() RepersistStore {
	if s.repersist != nil {
		return s.repersist
	}
	if r, ok := s.repo.(RepersistStore); ok {
		return r
	}
	return nil
}

// RepersistTask 把一个成功任务里仍是上游外链的产物搬回本地。任一槽位失败整体返回错误
// （已搬成功的槽位 asset 行留着，下次重试经 FindByTaskSlot 直接复用，不重复下载）。
func (s *Service) RepersistTask(ctx context.Context, userID, taskID int64) (*RepersistResult, error) {
	t, err := s.repo.Get(userID, taskID)
	if err != nil {
		return nil, err
	}
	if t.Status != "succeeded" {
		return nil, ErrNothingToRepersist
	}
	store := s.repersistStore()
	if store == nil || s.storage == nil {
		return nil, errors.New("repersist unavailable")
	}
	urls, assetIDs, reps, err := s.repersistTask(ctx, store, *t)
	if err != nil {
		return nil, err
	}
	sink, err := s.repo.SinkForTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("load sink: %w", err)
	}
	return &RepersistResult{Sink: sink, OutputURLs: urls, AssetIDs: assetIDs, Replacements: reps}, nil
}

func (s *Service) repersistTask(ctx context.Context, store RepersistStore, t Task) ([]string, []int64, []RepersistReplacement, error) {
	var output map[string]any
	if err := json.Unmarshal(t.Output, &output); err != nil {
		return nil, nil, nil, fmt.Errorf("output 不是合法 JSON: %w", err)
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

	var reps []RepersistReplacement
	outputKind := ""
	for i, raw := range urls {
		if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
			continue
		}
		newURL, assetID, mime, err := s.repersistSlot(ctx, t.UserID, t.ID, pid, i, raw, durMs)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("slot=%d: %w", i, err)
		}
		urls[i] = newURL
		assetIDs[i] = assetID
		if i == 0 {
			outputKind = outputKindFromMime(mime)
		}
		reps = append(reps, RepersistReplacement{Index: i, OldURL: raw, NewURL: newURL, AssetID: assetID})
	}
	if len(reps) == 0 {
		return nil, nil, nil, ErrNothingToRepersist
	}
	output["outputUrls"] = urls
	output["assetIds"] = assetIDs
	// 已搬回本地的槽位清掉首搬留下的失败原因；全部清空就整个去掉。
	if _, ok := output["persistErrors"]; ok {
		errs := anyStrings(output["persistErrors"])
		for _, rep := range reps {
			if rep.Index < len(errs) {
				errs[rep.Index] = ""
			}
		}
		if anyNonEmpty(errs) {
			output["persistErrors"] = errs
		} else {
			delete(output, "persistErrors")
		}
	}
	if outputKind != "" {
		output["outputKind"] = outputKind
	}
	next, err := json.Marshal(output)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := store.UpdateOutput(t.ID, next, outputKind); err != nil {
		return nil, nil, nil, fmt.Errorf("回写 output: %w", err)
	}
	return urls, assetIDs, reps, nil
}

// repersistSlot 先查槽位复用(上一轮可能对象已传成功、只是后面的回写失败),再走 persistSlot。
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
