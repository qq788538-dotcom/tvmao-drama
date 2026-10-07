// Package exitcode 把服务端稳定错误码映射成进程退出码。
//
// 脚本与 Agent 靠退出码分支，所以这张表是公共契约的一部分：
//
//	0 成功 / 2 参数或本地配置 / 3 鉴权失败（本地版一般不会出现） / 4 资源不存在
//	5 冲突（含歧义、版本、幂等） / 6 服务端或上游失败 / 7 网络超时或用户取消
//	8 喵剧未运行 / 9 网络被 AI 助手的沙箱拦截
package exitcode

const (
	OK       = 0
	Usage    = 2
	Auth     = 3
	NotFound = 4
	Conflict = 5
	Server   = 6
	Network  = 7
	// AppNotRunning：喵剧未运行（地址文件不存在、连接被拒、端口上不是本应用、token 重读后仍被拒）。
	AppNotRunning = 8
	// SandboxBlocked：网络被 AI 助手的沙箱拦截（CLI spec 3.4）。
	SandboxBlocked = 9
)

// FromAPIError 按错误码映射；未知码退回按 HTTP 状态推断。
//
// 分类依据是「用户该怎么办」而不是错误的技术性质：
//   - 2 = 改命令再来；3 = 鉴权失败（本地版一般不会出现）；4 = 换个目标；5 = 处理冲突后再来；
//   - 6 = 不是你的错，稍后或联系管理员；7 = 网络问题，可直接重试。
//
// INSUFFICIENT_BALANCE 归 6 而不是 2：用户改命令没用，得去充值。
func FromAPIError(code string, status int) int {
	switch code {
	case "AUTH_REQUIRED", "TOKEN_INVALID", "TOKEN_EXPIRED", "TOKEN_REVOKED", "AUDIENCE_DENIED":
		return Auth
	case "PROJECT_NOT_FOUND", "NODE_NOT_FOUND", "EDGE_NOT_FOUND", "MODEL_NOT_FOUND", "ASSET_NOT_FOUND":
		return NotFound
	case "NODE_ALREADY_RUNNING", "CANVAS_VERSION_CONFLICT", "IDEMPOTENCY_KEY_REUSED",
		"IDEMPOTENCY_IN_PROGRESS", "MODEL_REF_AMBIGUOUS":
		return Conflict
	case "INVALID_ARGUMENT", "INVALID_NODE_TYPE", "NODE_READONLY_TYPE",
		"INVALID_MODEL_PARAMS", "MODEL_REQUIRED":
		return Usage
	case "MODEL_DISABLED", "MODEL_UNAVAILABLE", "INSUFFICIENT_BALANCE",
		"UPSTREAM_UNAVAILABLE", "TASK_FAILED", "INTERNAL":
		return Server
	case "RATE_LIMITED":
		return Network
	}
	return fromStatus(status)
}

func fromStatus(status int) int {
	switch {
	case status == 401 || status == 403:
		return Auth
	case status == 404:
		return NotFound
	case status == 409:
		return Conflict
	case status == 429:
		return Network
	case status >= 500:
		return Server
	case status >= 400:
		return Usage
	default:
		return Server
	}
}
