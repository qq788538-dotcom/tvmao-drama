// Package client 是 /api/v1/cli/* 的 typed HTTP 客户端。
//
// 重试策略是这层最要紧的决定：**写请求只有携带 Idempotency-Key 时才允许重试**。
// 否则一次网络抖动重发「运行节点」，就是第二次跑上游。
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/james/huabu_studio/apps/cli/internal/endpoint"
)

const (
	// apiPrefix 是 CLI 契约的固定前缀。server 是地址文件给出的 origin，这里负责拼。
	apiPrefix = "/api/v1/cli"

	dialTimeout    = 10 * time.Second
	requestTimeout = 30 * time.Second
	// uploadTimeout 单独放宽：30 秒传不完一个视频。第二阶段直传对象存储后另算。
	uploadTimeout = 5 * time.Minute

	maxRetries  = 3
	baseBackoff = 300 * time.Millisecond
	maxBackoff  = 5 * time.Second
)

// APIError 是服务端返回的结构化错误。CLI 按 Code 分支，不解析 Message。
type APIError struct {
	Code      string
	Message   string
	RequestID string
	Status    int
	Retryable bool
	Details   map[string]any
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NetworkError 表示请求没能得到服务端响应（连不上、超时、被取消）。
// 与 APIError 分开，因为它映射到不同的退出码，且**永远可重试**。
type NetworkError struct {
	Err error
}

func (e *NetworkError) Error() string { return e.Err.Error() }
func (e *NetworkError) Unwrap() error { return e.Err }

// AppNotRunningError：地址文件不存在、连接被拒，或端口上不是本应用。
type AppNotRunningError struct{ Err error }

func (e *AppNotRunningError) Error() string { return "喵剧未运行，请先打开应用" }
func (e *AppNotRunningError) Unwrap() error { return e.Err }

// SandboxBlockedError：AI 助手的沙箱拦截了网络（CLI spec 3.4、3.5）。必须与"未运行"区分，
// 否则 AI 助手会以为应用没开，而不是去申请网络权限。
type SandboxBlockedError struct{ Err error }

func (e *SandboxBlockedError) Error() string {
	return "网络被 AI 助手的沙箱拦截，请允许本命令访问网络后重试"
}
func (e *SandboxBlockedError) Unwrap() error { return e.Err }

func classifyNetErr(err error) error {
	if isPermDenied(err) {
		return &SandboxBlockedError{Err: err}
	}
	if isConnRefused(err) {
		return &AppNotRunningError{Err: err}
	}
	return &NetworkError{Err: err}
}

type Client struct {
	// server / token 由 connect 从地址文件读出后赋值；应用重启后会换。
	server    string
	token     string
	resolve   func() (endpoint.Info, error)
	warn      func(string)
	version   string
	connected bool
	// reconnected：本条命令已经重读过一次地址文件（401 或连接被拒触发，两者共用），不再重连，避免来回打转。
	reconnected bool
	userAgent   string
	requestID   string
	hc          *http.Client
	// trace 打开后把请求行写进 stderr。**永不打印 Authorization 与 body**——
	// body 里有提示词和签名 URL。
	trace func(format string, a ...any)
}

type Options struct {
	// Resolve 读本机地址文件（endpoint.Load）。每次连接/401 后重读都会调用，不缓存。
	Resolve   func() (endpoint.Info, error)
	Version   string
	RequestID string
	// HTTPClient 供测试注入；nil 时用带超时的默认 client。
	HTTPClient *http.Client
	Trace      func(format string, a ...any)
	// Warn 输出非致命提示（如 CLI 与应用版本不一致）；nil 时不提示。
	Warn func(string)
}

func New(opts Options) *Client {
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
				TLSHandshakeTimeout:   dialTimeout,
				ResponseHeaderTimeout: requestTimeout,
				// 只连本机 127.0.0.1，不能走用户的 HTTP 代理。
				Proxy: nil,
			},
		}
	}
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	return &Client{
		resolve:   opts.Resolve,
		warn:      opts.Warn,
		version:   version,
		userAgent: "miaoju-cli/" + version,
		requestID: opts.RequestID,
		hc:        hc,
		trace:     opts.Trace,
	}
}

const pingApp = "miaoju-desktop"

type pingResult struct {
	App     string `json:"app"`
	Version string `json:"version"`
}

// connect 读地址文件并 ping 一次，确认端口上确实是本应用。refresh=true 表示上一次的 token 被拒，重读后再试。
func (c *Client) connect(ctx context.Context, refresh bool) error {
	if c.resolve == nil {
		return &AppNotRunningError{Err: errors.New("未配置地址解析")}
	}
	info, err := c.resolve()
	if errors.Is(err, endpoint.ErrNotRunning) {
		return &AppNotRunningError{Err: err}
	}
	if err != nil {
		return err
	}
	c.server, c.token = info.BaseURL(), info.Token
	var p pingResult
	if err := c.attemptJSON(ctx, http.MethodGet, "/ping", &p); err != nil {
		var api *APIError
		if errors.As(err, &api) && api.Status == http.StatusUnauthorized {
			if !refresh {
				return c.connect(ctx, true)
			}
			return &AppNotRunningError{Err: err}
		}
		// 连接层错误已分类，原样返回；其余（ping 返回 404、200 但不是 JSON 等）
		// 说明端口上不是喵剧（常见于应用崩溃后端口被别的程序占用）。
		var ne *NetworkError
		var anr *AppNotRunningError
		var sb *SandboxBlockedError
		if errors.As(err, &ne) || errors.As(err, &anr) || errors.As(err, &sb) {
			return err
		}
		return &AppNotRunningError{Err: err}
	}
	if p.App != pingApp {
		return &AppNotRunningError{Err: fmt.Errorf("端口 %d 上不是喵剧（app=%q）", info.Port, p.App)}
	}
	if !c.connected && c.warn != nil && c.version != "" && c.version != "dev" &&
		info.AppVersion != "" && info.AppVersion != c.version {
		c.warn(fmt.Sprintf("命令行工具版本 %s 与喵剧 %s 不一致，请在喵剧 设置 → 命令行工具 中重新安装", c.version, info.AppVersion))
	}
	c.connected = true
	return nil
}

// attemptJSON 发一次请求（不重试），≥400 走 decodeAPIError，否则把 body 解到 out。
func (c *Client) attemptJSON(ctx context.Context, method, path string, out any) error {
	return c.attempt(ctx, request{method: method, path: path}, nil, out)
}

// request 描述一次调用。
type request struct {
	method string
	path   string // 不含 apiPrefix
	query  url.Values
	body   any
	// idempotencyKey 非空时：① 发 Idempotency-Key 头；② 允许写请求重试。
	idempotencyKey string
	ifMatchVersion int64
	// longTimeout 给上传用。
	longTimeout bool
	// rawBody / contentType 供 multipart 上传绕过 JSON 编码。
	rawBody     []byte
	contentType string
}

// do 确保已连上本机应用后执行请求；遇 401 或（已连上后）连接被拒时重读地址文件再试一次，
// 每条命令最多重连一次。
func (c *Client) do(ctx context.Context, req request, out any) error {
	if !c.connected {
		if err := c.connect(ctx, false); err != nil {
			return err
		}
	}
	err := c.doOnce(ctx, req, out)
	var anr *AppNotRunningError
	if c.connected && !c.reconnected && errors.As(err, &anr) && isConnRefused(anr.Err) {
		// 应用崩溃重启后端口换了：旧端口是连接被拒而不是 401。重读地址文件，端口确实变了才重发
		c.reconnected = true
		old := c.server
		if cerr := c.connect(ctx, true); cerr != nil || c.server == old {
			return err
		}
		return c.doOnce(ctx, req, out)
	}
	var api *APIError
	if errors.As(err, &api) && api.Status == http.StatusUnauthorized {
		if c.reconnected {
			return &AppNotRunningError{Err: err}
		}
		// 应用重启过：端口和 token 都换了，重读地址文件再试一次（不占用 maxRetries）
		c.reconnected = true
		if cerr := c.connect(ctx, true); cerr != nil {
			return cerr
		}
		err = c.doOnce(ctx, req, out)
		if errors.As(err, &api) && api.Status == http.StatusUnauthorized {
			return &AppNotRunningError{Err: err}
		}
	}
	return err
}

// doOnce 执行请求（含退避重试）并把响应 JSON 解进 out（out 为 nil 时丢弃 body）。
func (c *Client) doOnce(ctx context.Context, req request, out any) error {
	var payload []byte
	if req.rawBody != nil {
		payload = req.rawBody
	} else if req.body != nil {
		blob, err := json.Marshal(req.body)
		if err != nil {
			return err
		}
		payload = blob
	}

	retryable := isSafeMethod(req.method) || req.idempotencyKey != ""

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			wait := backoff(attempt, lastErr)
			c.tracef("retry #%d after %v", attempt, wait)
			select {
			case <-ctx.Done():
				return &NetworkError{Err: ctx.Err()}
			case <-time.After(wait):
			}
		}
		err := c.attempt(ctx, req, payload, out)
		if err == nil {
			return nil
		}
		lastErr = err

		// 用户取消 / ctx 超时不重试——重试只会拖长用户等待。
		if ctx.Err() != nil {
			return &NetworkError{Err: ctx.Err()}
		}
		if !retryable || !shouldRetry(err) {
			return err
		}
	}
	return lastErr
}

func (c *Client) attempt(ctx context.Context, req request, payload []byte, out any) error {
	u := c.server + apiPrefix + req.path
	if len(req.query) > 0 {
		u += "?" + req.query.Encode()
	}

	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.method, u, bodyReader)
	if err != nil {
		return err
	}
	if payload != nil {
		ct := req.contentType
		if ct == "" {
			ct = "application/json"
		}
		httpReq.Header.Set("Content-Type", ct)
	}
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}
	httpReq.Header.Set("User-Agent", c.userAgent)
	if c.requestID != "" {
		httpReq.Header.Set("X-Request-ID", c.requestID)
	}
	if req.idempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", req.idempotencyKey)
	}
	if req.ifMatchVersion > 0 {
		httpReq.Header.Set("If-Match", fmt.Sprintf("\"snapshot-%d\"", req.ifMatchVersion))
	}

	// 只打方法与路径。token 在 header 里、提示词与签名 URL 在 body 里，都不能进日志。
	c.tracef("%s %s", req.method, req.path)

	hc := c.hc
	if req.longTimeout && hc.Timeout > 0 && hc.Timeout < uploadTimeout {
		clone := *hc
		clone.Timeout = uploadTimeout
		hc = &clone
	}

	resp, err := hc.Do(httpReq)
	if err != nil {
		return classifyNetErr(err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &NetworkError{Err: err}
	}
	if resp.StatusCode >= 400 {
		return decodeAPIError(resp, respBody)
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	return json.Unmarshal(respBody, out)
}

func decodeAPIError(resp *http.Response, body []byte) error {
	var env struct {
		Error struct {
			Code      string         `json:"code"`
			Message   string         `json:"message"`
			Retryable bool           `json:"retryable"`
			RequestID string         `json:"requestId"`
			Details   map[string]any `json:"details"`
		} `json:"error"`
	}
	e := &APIError{Status: resp.StatusCode, RequestID: resp.Header.Get("X-Request-ID")}
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Code != "" {
		e.Code = env.Error.Code
		e.Message = env.Error.Message
		e.Retryable = env.Error.Retryable
		e.Details = env.Error.Details
		if env.Error.RequestID != "" {
			e.RequestID = env.Error.RequestID
		}
		return e
	}
	// 非 CLI 契约的错误体（如 nginx 502 的 HTML）：保留状态码，消息截断。
	e.Message = strings.TrimSpace(string(body))
	if len(e.Message) > 200 {
		// 按字节截断会把中文切成半个字符（非法 UTF-8），退到字符边界。
		cut := 200
		for cut > 0 && !utf8.RuneStart(e.Message[cut]) {
			cut--
		}
		e.Message = e.Message[:cut] + "…"
	}
	if e.Message == "" {
		e.Message = resp.Status
	}
	return e
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead
}

// shouldRetry：网络错误恒可重试；HTTP 错误只信服务端自己标的 retryable，
// 外加 429/502/503/504 这几个「肯定是基础设施而不是业务」的状态。
func shouldRetry(err error) bool {
	var anr *AppNotRunningError
	var sb *SandboxBlockedError
	if errors.As(err, &anr) || errors.As(err, &sb) {
		return false // 重试无意义，只会让用户多等几秒
	}
	var ne *NetworkError
	if errors.As(err, &ne) {
		return true
	}
	var ae *APIError
	if errors.As(err, &ae) {
		if ae.Retryable {
			return true
		}
		switch ae.Status {
		case http.StatusTooManyRequests, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
	}
	return false
}

// backoff 指数退避 + 抖动；服务端给了 Retry-After 就听它的。
func backoff(attempt int, lastErr error) time.Duration {
	if d, ok := retryAfter(lastErr); ok {
		return d
	}
	wait := baseBackoff * time.Duration(1<<uint(attempt-1))
	if wait > maxBackoff {
		wait = maxBackoff
	}
	// 抖动避免多个并发命令同时重试把服务端再打一次。
	return wait + time.Duration(rand.Int63n(int64(wait/2+1)))
}

func retryAfter(err error) (time.Duration, bool) {
	var ae *APIError
	if !errors.As(err, &ae) || ae.Details == nil {
		return 0, false
	}
	raw, ok := ae.Details["retryAfterSeconds"]
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case float64:
		return time.Duration(v) * time.Second, true
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second, true
		}
	}
	return 0, false
}

func (c *Client) tracef(format string, a ...any) {
	if c.trace != nil {
		c.trace(format, a...)
	}
}
