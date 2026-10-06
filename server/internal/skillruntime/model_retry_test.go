package skillruntime

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
)

// 生产 2026-07-21/22 的真实死法:429(用户并发满)/524(代理 120s 读超时)只重试
// 2 次、亚秒退避,烧完把跑了十几分钟的 Run 整场判死。这里锁住重试分类与退避节奏。

func TestIsRetryableModelErrorByStatusCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"429 并发满(typed)", &openai.APIError{HTTPStatusCode: 429, Message: "Concurrency limit exceeded for user"}, true},
		{"524 代理读超时(typed)", &openai.APIError{HTTPStatusCode: 524, Message: "origin took too long"}, true},
		{"500 上游内部错(typed)", &openai.APIError{HTTPStatusCode: 500}, true},
		{"408 请求超时(typed)", &openai.APIError{HTTPStatusCode: 408}, true},
		{"401 坏 key 重试无意义", &openai.APIError{HTTPStatusCode: 401}, false},
		{"400 业务错误重试无意义", &openai.APIError{HTTPStatusCode: 400, Message: "context length exceeded"}, false},
		{"404 模型名错误重试无意义", &openai.APIError{HTTPStatusCode: 404}, false},
		// RequestError 等非 typed 包装只留下字符串——必须从文案里认出状态码
		{"524 字符串兜底", errors.New("error, status code: 524, status: 524 status code 524, message: The origin web server did not return a complete response"), true},
		{"429 字符串兜底", errors.New("error, status code: 429, status: 429 Too Many Requests, message: Concurrency limit exceeded for user, please retry later"), true},
		{"403 字符串兜底", errors.New("error, status code: 403, status: 403 Forbidden, message: nope"), false},
		{"包了一层的 typed 错误", fmt.Errorf("call model: %w", &openai.APIError{HTTPStatusCode: 429}), true},
		{"无状态码网络错默认可重试", errors.New("read tcp 10.0.0.1:443: connection reset by peer"), true},
		{"nil 不可重试", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryableModelError(context.Background(), tc.err); got != tc.want {
				t.Fatalf("isRetryableModelError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestModelRetryBackoffFullJitterWithinCaps(t *testing.T) {
	// 全抖动:每次在 (0, 上限] 内随机。上限 5s 起每次翻倍、封顶 60s——
	// 429 的解除靠同用户其他分钟级请求结束腾名额,亚秒退避等于没退避。
	caps := map[int]time.Duration{
		1: 5 * time.Second,
		2: 10 * time.Second,
		3: 20 * time.Second,
		4: 40 * time.Second,
		5: 60 * time.Second,
		6: 60 * time.Second,
		9: 60 * time.Second, // 越界 attempt 不得溢出
	}
	for attempt, cap := range caps {
		var maxSeen time.Duration
		for i := 0; i < 200; i++ {
			d := modelRetryBackoff(context.Background(), attempt)
			if d <= 0 || d > cap {
				t.Fatalf("attempt %d: backoff %s 超出 (0, %s]", attempt, d, cap)
			}
			if d > maxSeen {
				maxSeen = d
			}
		}
		// 抖动必须真的在随机:200 次采样若全部挤在上限 1/4 以下,说明没按满区间抖
		if maxSeen < cap/4 {
			t.Fatalf("attempt %d: 200 次采样最大 %s,疑似没有全区间抖动(上限 %s)", attempt, maxSeen, cap)
		}
	}
}

func TestNewModelRetryConfigWiring(t *testing.T) {
	cfg := newModelRetryConfig()
	if cfg.MaxRetries != 6 {
		t.Fatalf("MaxRetries = %d, want 6", cfg.MaxRetries)
	}
	if cfg.IsRetryAble == nil || cfg.BackoffFunc == nil {
		t.Fatal("IsRetryAble/BackoffFunc 必须显式配置,不能落回 eino 默认(亚秒退避+全错误重试)")
	}
}
