package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/james/huabu_studio/apps/api/internal/cliapi"
)

// CLI HTTP 层的响应渲染。与网页的 writeError（{"error":"中文"}）刻意不同：
// CLI 调用方按稳定 code 分支决定退出码，不解析中文 message。
// 两套渲染并存是有意的——网页那套是给人看的表单错误，改它会波及所有前端页面。

type cliErrorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	RequestID string         `json:"requestId"`
	Details   map[string]any `json:"details,omitempty"`
}

type cliErrorEnvelope struct {
	Error cliErrorBody `json:"error"`
}

type cliRequestIDKey struct{}

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, cliRequestIDKey{}, id)
}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(cliRequestIDKey{}).(string)
	return id
}

// CLIRequestID 给每个 CLI 请求打上可追踪 id：调用方传了就沿用（便于 CLI 端
// 把一条命令的多次 HTTP 串起来），没传就生成。id 同时回写响应头与错误体，
// 这样用户贴一个 id 就能在服务端日志里定位。
func CLIRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(withRequestID(r.Context(), id)))
	})
}

func newRequestID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// 随机源失败不该让请求失败——退化成固定前缀，只影响可追踪性。
		return "req_norand"
	}
	return "req_" + hex.EncodeToString(buf)
}

// writeCLIJSON 输出成功响应。
func writeCLIJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeCLIError 渲染错误。非 *cliapi.Error 一律降级成 INTERNAL——
// 不让底层错误类型泄漏成公共契约的一部分。
func writeCLIError(w http.ResponseWriter, r *http.Request, err error) {
	var ce *cliapi.Error
	if !errors.As(err, &ce) {
		ce = cliapi.New(cliapi.CodeInternal, err.Error())
	}
	body := cliErrorEnvelope{Error: cliErrorBody{
		Code:      ce.Code,
		Message:   ce.Message,
		Retryable: ce.Retryable(),
		RequestID: requestIDFromContext(r.Context()),
		Details:   ce.Details,
	}}
	// 处理中的幂等重放要告诉调用方多久后再来，否则 CLI 只能盲目退避。
	if ce.Code == cliapi.CodeIdempotencyInProgress {
		w.Header().Set("Retry-After", "2")
	}
	writeCLIJSON(w, ce.Status(), body)
}
