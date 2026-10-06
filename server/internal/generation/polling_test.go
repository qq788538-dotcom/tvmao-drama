package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/upstream"
)

func TestPollUpstream_RetriesTemporaryErrorsAndEventuallySucceeds(t *testing.T) {
	caller := &fakePollCaller{
		responses: []fakePollResponse{
			{err: &upstream.CallError{StatusCode: http.StatusTooManyRequests, Body: "busy"}},
			{result: &upstream.CallResult{Body: json.RawMessage(`{"status":"completed","output":["https://cdn.example/ref.png"]}`)}},
		},
	}
	svc := &Service{pollRetryBase: time.Millisecond, pollRetryMax: 2 * time.Millisecond}

	result, err := svc.pollUpstream(context.Background(), caller, "sk-test", json.RawMessage(`{
		"method":"GET",
		"path":"/tasks/{{task_id}}",
		"interval":1,
		"maxWait":3,
		"statusField":"status",
		"completedValue":"completed"
	}`), "task-1", 0)
	if err != nil {
		t.Fatalf("pollUpstream: %v", err)
	}
	if caller.callCount() != 2 {
		t.Fatalf("call count = %d, want 2", caller.callCount())
	}
	if len(result.OutputURLs) != 1 || result.OutputURLs[0] != "https://cdn.example/ref.png" {
		t.Fatalf("output urls = %v", result.OutputURLs)
	}
}

// §2 分类表(v5,codex 实施审查 B2 修正):轮询 GET 的 4xx(任务不存在/参数错)是
// 「不确定」——上游"查不到"不等于"没在跑",必须保留 ID 按退避续轮,绝不首错即终败。
func TestPollUpstream_Poll4xxKeepsPollingUntilResult(t *testing.T) {
	caller := &fakePollCaller{
		responses: []fakePollResponse{
			{err: &upstream.CallError{StatusCode: http.StatusBadRequest, Body: "task not found yet"}},
			{err: &upstream.CallError{StatusCode: http.StatusNotFound, Body: "still not found"}},
			{result: &upstream.CallResult{Body: json.RawMessage(`{"status":"completed","output":["https://cdn.example/ref.png"]}`)}},
		},
	}
	svc := &Service{pollRetryBase: time.Millisecond, pollRetryMax: 2 * time.Millisecond}

	res, err := svc.pollUpstream(context.Background(), caller, "sk-test", json.RawMessage(`{
		"method":"GET",
		"path":"/tasks/{{task_id}}",
		"interval":1,
		"maxWait":3,
		"statusField":"status",
		"completedValue":"completed"
	}`), "task-1", 0)
	if err != nil {
		t.Fatalf("4xx 后续轮应最终拿到结果, err=%v", err)
	}
	if res == nil {
		t.Fatal("result nil")
	}
	if caller.callCount() != 3 {
		t.Fatalf("call count = %d, want 3(两次 4xx 退避后第三次成功)", caller.callCount())
	}
}

// 4xx 一直查不到 → 续轮到 max poll 窗口耗尽,以本地超时(不确定)收场——
// 由 completeAsync 走保守失败退款,绝不清 ID 重排。
func TestPollUpstream_Persistent4xxEndsWithWindowTimeout(t *testing.T) {
	caller := &fakePollCaller{repeatErr: &upstream.CallError{StatusCode: http.StatusNotFound, Body: "gone"}}
	svc := &Service{pollRetryBase: time.Millisecond, pollRetryMax: 2 * time.Millisecond}

	_, err := svc.pollUpstream(context.Background(), caller, "sk-test", json.RawMessage(`{
		"method":"GET",
		"path":"/tasks/{{task_id}}",
		"interval":1,
		"maxWait":2,
		"statusField":"status",
		"completedValue":"completed"
	}`), "task-1", 0)
	if err == nil {
		t.Fatal("窗口耗尽应报本地超时")
	}
	var term *upstreamTerminalError
	if errors.As(err, &term) {
		t.Fatalf("本地超时绝不能是权威终态(那会进业务重试清空标记), err=%v", err)
	}
	if caller.callCount() < 2 {
		t.Fatalf("应持续续轮, call count=%d", caller.callCount())
	}
}

type fakePollCaller struct {
	mu        sync.Mutex
	responses []fakePollResponse
	repeatErr error // responses 耗尽后恒返回(模拟持续 4xx)
	calls     int
}

type fakePollResponse struct {
	result *upstream.CallResult
	err    error
}

func (f *fakePollCaller) Call(context.Context, string, string, string, json.RawMessage) (*upstream.CallResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls >= len(f.responses) {
		f.calls++
		if f.repeatErr != nil {
			return nil, f.repeatErr
		}
		return nil, errors.New("unexpected poll call")
	}
	resp := f.responses[f.calls]
	f.calls++
	if resp.err != nil {
		return nil, resp.err
	}
	return resp.result, nil
}

func (f *fakePollCaller) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestMaxWaitSecondsParsesTemplate(t *testing.T) {
	cases := []struct {
		template string
		want     int
	}{
		{`{"maxWait":1800}`, 1800},
		{`{}`, 300},
		{`not-json`, 300},
		// 后台配 24h（超低价上游的超长排队）要完整生效。
		{`{"maxWait":86400}`, 86400},
		// 超过 24h 的部分按上限生效——上限在窗口收口，不再靠 ctx 静默掐。
		{`{"maxWait":172800}`, 86400},
	}
	for _, tc := range cases {
		if got := maxWaitSeconds(json.RawMessage(tc.template)); got != tc.want {
			t.Fatalf("maxWaitSeconds(%s) = %d, want %d", tc.template, got, tc.want)
		}
	}
}

func TestRemainingPollWindowScalesByTaskAge(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		startedAt time.Time
		want      time.Duration
	}{
		{"新任务给全窗口", now, 1800 * time.Second},
		{"跑了10分钟剩20分钟", now.Add(-10 * time.Minute), 20 * time.Minute},
		{"超龄保底2分钟", now.Add(-2 * time.Hour), 2 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := remainingPollWindow(tc.startedAt, 1800)
			diff := got - tc.want
			if diff < -2*time.Second || diff > 2*time.Second {
				t.Fatalf("remainingPollWindow = %v, want ≈%v", got, tc.want)
			}
		})
	}
}

func TestPollCtxTimeoutAddsBufferAndClamps(t *testing.T) {
	if got := pollCtxTimeout(30 * time.Minute); got != 32*time.Minute {
		t.Fatalf("pollCtxTimeout(30m) = %v", got)
	}
	// clamp 必须比窗口上限（24h）大，否则 ctx 越权成了真正的限流者——
	// 曾经它是 4h，把后台配的 24h 静默压成 4h，用户看到的是莫名其妙的
	// 「生成超时」退款（2026-07-29 生产 mini-mg 实证）。
	if got := pollCtxTimeout(24 * time.Hour); got != 24*time.Hour+2*time.Minute {
		t.Fatalf("pollCtxTimeout(24h) = %v，24h 窗口的友好文案必须先于 ctx 触发", got)
	}
	if got := pollCtxTimeout(48 * time.Hour); got != 24*time.Hour+2*time.Minute {
		t.Fatalf("pollCtxTimeout(48h) = %v", got)
	}
}

func TestPollUpstream_WindowOverrideProducesFriendlyTimeout(t *testing.T) {
	processing := fakePollResponse{result: &upstream.CallResult{Body: json.RawMessage(`{"status":"processing"}`)}}
	caller := &fakePollCaller{responses: []fakePollResponse{processing, processing, processing}}
	svc := &Service{pollRetryBase: time.Millisecond, pollRetryMax: 2 * time.Millisecond}

	_, err := svc.pollUpstream(context.Background(), caller, "sk-test", json.RawMessage(`{
		"method":"GET",
		"path":"/tasks/{{task_id}}",
		"interval":1,
		"maxWait":300,
		"statusField":"status",
		"completedValue":"completed"
	}`), "task-1", time.Second)
	if err == nil {
		t.Fatal("pollUpstream error = nil, want window timeout")
	}
	if !strings.Contains(err.Error(), "polling timeout after 1s") {
		t.Fatalf("err = %v, want friendly window timeout", err)
	}
}

func TestPollFailureMessageTranslatesDeadline(t *testing.T) {
	if got := pollFailureMessage(context.DeadlineExceeded); got != "生成超时，请重试" {
		t.Fatalf("deadline message = %q", got)
	}
	if got := pollFailureMessage(errors.New("upstream 500")); got != "upstream 500" {
		t.Fatalf("normal error must pass through, got %q", got)
	}
	wrapped := fmt.Errorf("poll: %w", context.DeadlineExceeded)
	if got := pollFailureMessage(wrapped); got != "生成超时，请重试" {
		t.Fatalf("wrapped deadline message = %q", got)
	}
}
