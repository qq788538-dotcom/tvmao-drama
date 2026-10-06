package skillruntime

import (
	"context"
	"errors"
	"math/rand/v2"
	"regexp"
	"strconv"
	"time"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
)

// 模型层原地重试配置。这一层失败的替代品是外层整场 Run 报废重跑
// （已完成的十几轮上下文全丢,重排队再烧 15~20 分钟),所以宁可在这里
// 多等几分钟也不能轻易放弃。eino 默认(2 次、100ms 起步退避)对生产两大
// 死因完全无效:429「用户并发满」要等同用户其他分钟级请求结束腾名额,
// 524 是代理 120s 读超时——亚秒重试撞回去还是同样的墙。
func newModelRetryConfig() *adk.ModelRetryConfig {
	return &adk.ModelRetryConfig{
		MaxRetries:  6,
		IsRetryAble: isRetryableModelError,
		BackoffFunc: modelRetryBackoff,
	}
}

// statusCodeRe 从错误文案里兜底抠 HTTP 状态码。go-openai 的 RequestError
// (上游返回非 JSON,比如 Cloudflare 524 的 HTML)不会被 eino-ext 转成 typed
// APIError,穿到这里只剩 "error, status code: NNN, ..." 字符串。
var statusCodeRe = regexp.MustCompile(`status code: (\d{3})`)

func isRetryableModelError(_ context.Context, err error) bool {
	if err == nil {
		return false
	}
	if status, ok := httpStatusFromError(err); ok {
		// 429/408/5xx(含 524)是「等一等就好」;其余 4xx(坏 key、超长 prompt、
		// 错模型名)重试多少次都一样,立即失败让外层带着原因退款/终判。
		return status == 429 || status == 408 || status >= 500
	}
	// 没有状态码 = 网络层错误(连接重置/EOF/DNS),默认可重试。
	return true
}

func httpStatusFromError(err error) (int, bool) {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatusCode > 0 {
		return apiErr.HTTPStatusCode, true
	}
	if m := statusCodeRe.FindStringSubmatch(err.Error()); m != nil {
		if status, convErr := strconv.Atoi(m[1]); convErr == nil {
			return status, true
		}
	}
	return 0, false
}

// modelRetryBackoff 全抖动指数退避:上限 5s×2^(n-1) 封顶 60s,实际等待在
// (0, 上限] 内均匀随机。全抖动不是锦上添花:429 是一批并发 Run 同时撞出来的,
// 固定等待会让它们同拍重试再次集体 429,永远踩不开。
func modelRetryBackoff(_ context.Context, attempt int) time.Duration {
	const (
		base = 5 * time.Second
		max  = 60 * time.Second
	)
	cap := max
	if attempt >= 1 && attempt <= 4 {
		cap = base << uint(attempt-1)
	}
	if cap > max {
		cap = max
	}
	return time.Duration(1 + rand.Int64N(int64(cap)))
}
