package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"sync"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

// modelConnTestClient 是“测试连接”用的 HTTP 客户端；包级变量，测试可替换。
var modelConnTestClient = &nethttp.Client{Timeout: 15 * time.Second}

const (
	modelConnTestConcurrency = 4
	modelConnMaxBodyRunes    = 200
)

type modelConnResult struct {
	ID      int64  `json:"id"`
	ModelID string `json:"modelId"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type connGroupKey struct{ baseURL, apiKey string }

// testConnection 逐个解析每个模型的上游，按 (地址, Key) 分组，每组只 GET 一次 /v1/models，
// 再按模型 ID 是否在返回列表里判定。不发任何计费的生成请求。
func (h *modelSettingsHandler) testConnection(w nethttp.ResponseWriter, r *nethttp.Request) {
	list, err := h.d.Models.List(false)
	if err != nil {
		scWriteError(w, err)
		return
	}
	results := make([]modelConnResult, len(list))
	groups := map[connGroupKey][]int{}
	var order []connGroupKey
	for i := range list {
		m := &list[i]
		results[i] = modelConnResult{ID: m.ID, ModelID: m.ModelID}
		base, key, err := models.ResolveUpstream(m, h.d.Settings)
		if err != nil {
			results[i].Message = "读取上游配置失败：" + redactKey(err.Error(), key)
			continue
		}
		base, key = strings.TrimSpace(base), strings.TrimSpace(key)
		if base == "" || key == "" {
			results[i].Message = "未配置上游地址或 API Key"
			continue
		}
		k := connGroupKey{base, key}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], i)
	}

	sem := make(chan struct{}, modelConnTestConcurrency)
	var wg sync.WaitGroup
	for _, k := range order {
		wg.Add(1)
		sem <- struct{}{}
		go func(k connGroupKey, idxs []int) {
			defer wg.Done()
			defer func() { <-sem }()
			ids, errMsg := fetchUpstreamModelIDs(r.Context(), k.baseURL, k.apiKey)
			for _, i := range idxs {
				switch {
				case errMsg != "":
					results[i].Message = errMsg
				case ids[results[i].ModelID]:
					results[i].OK, results[i].Message = true, "成功"
				default:
					results[i].Message = fmt.Sprintf("网关上没有这个模型（%s）", results[i].ModelID)
				}
			}
		}(k, groups[k])
	}
	wg.Wait()
	writeJSON(w, nethttp.StatusOK, map[string]any{"results": results})
}

// fetchUpstreamModelIDs 请求 {base}/v1/models，成功返回模型 ID 集合；失败返回已脱敏的中文原因。
func fetchUpstreamModelIDs(ctx context.Context, baseURL, apiKey string) (map[string]bool, string) {
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, baseURL+"/v1/models", nil)
	if err != nil {
		return nil, "连接失败：" + redactKey(err.Error(), apiKey)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := modelConnTestClient.Do(req)
	if err != nil {
		return nil, "连接失败：" + redactKey(err.Error(), apiKey)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, "连接失败：" + redactKey(err.Error(), apiKey)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		msg := strings.TrimSpace(string(body))
		if json.Unmarshal(body, &e) == nil && strings.TrimSpace(e.Error.Message) != "" {
			msg = strings.TrimSpace(e.Error.Message)
		}
		// 先脱敏再截断，避免 Key 被截成残片后漏过替换。
		msg = redactKey(msg, apiKey)
		if rs := []rune(msg); len(rs) > modelConnMaxBodyRunes {
			msg = string(rs[:modelConnMaxBodyRunes])
		}
		return nil, fmt.Sprintf("HTTP %d：%s", resp.StatusCode, msg)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "连接失败：响应不是有效的模型列表"
	}
	ids := make(map[string]bool, len(payload.Data))
	for _, d := range payload.Data {
		ids[d.ID] = true
	}
	return ids, ""
}

func redactKey(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "***")
}
