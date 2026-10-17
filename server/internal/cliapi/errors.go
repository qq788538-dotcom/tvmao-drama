// Package cliapi 是自由画布 CLI 的服务端适配层：稳定 DTO、语义节点映射、
// 幂等与领域编排。它与网页 HTTP 层完全独立，但 service 层及以下
// 全部复用现有 projects/canvasstate/canvasgen/models。
//
// 错误码是公共契约。CLI 按 code 分支决定退出码，**不解析中文 message**，
// 所以：只增不改语义；改语义必须开 /api/v2/cli。
package cliapi

import "fmt"

// CLI 稳定错误码。
const (
	// 鉴权
	CodeAuthRequired   = "AUTH_REQUIRED"
	CodeTokenInvalid   = "TOKEN_INVALID"
	CodeTokenExpired   = "TOKEN_EXPIRED"
	CodeTokenRevoked   = "TOKEN_REVOKED"   // 改密后旧 token 失效
	CodeAudienceDenied = "AUDIENCE_DENIED" // 网页 token 调 CLI API，或反向

	// 资源
	CodeProjectNotFound = "PROJECT_NOT_FOUND"
	CodeNodeNotFound    = "NODE_NOT_FOUND"
	CodeEdgeNotFound    = "EDGE_NOT_FOUND"
	CodeModelNotFound   = "MODEL_NOT_FOUND"
	CodeAssetNotFound   = "ASSET_NOT_FOUND"

	// 请求语义
	CodeInvalidArgument    = "INVALID_ARGUMENT"
	CodeInvalidNodeType    = "INVALID_NODE_TYPE"
	CodeNodeReadonlyType   = "NODE_READONLY_TYPE" // 试图写入 4 种可写类型以外的节点
	CodeInvalidModelParams = "INVALID_MODEL_PARAMS"
	CodeModelRequired      = "MODEL_REQUIRED" // 创建生成节点未指定模型，或存量节点没有 modelId
	CodeModelDisabled      = "MODEL_DISABLED"
	CodeModelUnavailable   = "MODEL_UNAVAILABLE"

	// 冲突
	CodeNodeAlreadyRunning    = "NODE_ALREADY_RUNNING"
	CodeCanvasVersionConflict = "CANVAS_VERSION_CONFLICT"
	CodeIdempotencyKeyReused  = "IDEMPOTENCY_KEY_REUSED"
	CodeIdempotencyInProgress = "IDEMPOTENCY_IN_PROGRESS"
	CodeModelRefAmbiguous     = "MODEL_REF_AMBIGUOUS"

	// 服务端 / 上游
	CodeRateLimited         = "RATE_LIMITED"
	CodeUpstreamUnavailable = "UPSTREAM_UNAVAILABLE"
	CodeTaskFailed          = "TASK_FAILED"
	CodeInternal            = "INTERNAL"
)

// Error 是 CLI API 的统一错误。Details 里放机器可读的补充信息
// （如歧义候选列表），message 只给人看。
type Error struct {
	Code    string
	Message string
	Details map[string]any
	// Err 是被包裹的底层错误，只进服务端日志，不返给调用方——
	// 底层错误常含上游地址或 SQL 片段。
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// Status 返回该错误对应的 HTTP 状态码。
func (e *Error) Status() int { return StatusFor(e.Code) }

// Retryable 表示 CLI 是否可以自动重试。判错的后果是双向的：
// 把业务性拒绝标成可重试会无限打上游；把 UPSTREAM_UNAVAILABLE
// 标成不可重试会让一次抖动直接失败。
func (e *Error) Retryable() bool {
	switch e.Code {
	case CodeRateLimited, CodeUpstreamUnavailable, CodeIdempotencyInProgress, CodeInternal:
		return true
	default:
		return false
	}
}

func (e *Error) WithDetails(d map[string]any) *Error {
	e.Details = d
	return e
}

func (e *Error) WithCause(err error) *Error {
	e.Err = err
	return e
}

func New(code, message string) *Error { return &Error{Code: code, Message: message} }

func Errf(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// StatusFor 把错误码映射成 HTTP 状态。未知码一律 500——宁可暴露成服务端问题，
// 也不要伪装成 4xx 让调用方以为是自己的锅。
func StatusFor(code string) int {
	switch code {
	case CodeAuthRequired, CodeTokenInvalid, CodeTokenExpired, CodeTokenRevoked, CodeAudienceDenied:
		return 401
	case CodeProjectNotFound, CodeNodeNotFound, CodeEdgeNotFound, CodeModelNotFound, CodeAssetNotFound:
		return 404
	case CodeInvalidArgument, CodeInvalidNodeType, CodeNodeReadonlyType, CodeInvalidModelParams,
		CodeModelRequired, CodeModelDisabled, CodeModelUnavailable:
		return 400
	case CodeNodeAlreadyRunning, CodeCanvasVersionConflict, CodeIdempotencyKeyReused,
		CodeIdempotencyInProgress, CodeModelRefAmbiguous:
		return 409
	case CodeRateLimited:
		return 429
	case CodeUpstreamUnavailable, CodeTaskFailed:
		return 502
	default:
		return 500
	}
}
