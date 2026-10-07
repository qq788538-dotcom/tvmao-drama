package exitcode

import "testing"

func TestFromAPIError(t *testing.T) {
	cases := map[string]int{
		"AUTH_REQUIRED": Auth, "TOKEN_INVALID": Auth, "TOKEN_EXPIRED": Auth,
		"TOKEN_REVOKED": Auth, "AUDIENCE_DENIED": Auth,

		"PROJECT_NOT_FOUND": NotFound, "NODE_NOT_FOUND": NotFound,
		"EDGE_NOT_FOUND": NotFound, "MODEL_NOT_FOUND": NotFound, "ASSET_NOT_FOUND": NotFound,

		"NODE_ALREADY_RUNNING": Conflict, "CANVAS_VERSION_CONFLICT": Conflict,
		"IDEMPOTENCY_KEY_REUSED": Conflict, "IDEMPOTENCY_IN_PROGRESS": Conflict,
		"MODEL_REF_AMBIGUOUS": Conflict,

		"INVALID_ARGUMENT": Usage, "INVALID_NODE_TYPE": Usage, "NODE_READONLY_TYPE": Usage,
		"INVALID_MODEL_PARAMS": Usage, "MODEL_REQUIRED": Usage,

		"MODEL_DISABLED": Server, "MODEL_UNAVAILABLE": Server, "UPSTREAM_UNAVAILABLE": Server,
		"TASK_FAILED": Server, "INTERNAL": Server,

		"RATE_LIMITED": Network,
	}
	for code, want := range cases {
		if got := FromAPIError(code, 0); got != want {
			t.Errorf("FromAPIError(%s) = %d, want %d", code, got, want)
		}
	}
}

// 该业务码归 6 而不是 2：用户改命令没用。
func TestInsufficientBalanceIsServerNotUsage(t *testing.T) {
	if got := FromAPIError("INSUFFICIENT_BALANCE", 400); got != Server {
		t.Fatalf("= %d, want %d", got, Server)
	}
}

func TestFromAPIErrorUnknownCodeFallsBackToStatus(t *testing.T) {
	cases := map[int]int{
		401: Auth, 403: Auth, 404: NotFound, 409: Conflict,
		429: Network, 500: Server, 502: Server, 400: Usage, 422: Usage, 0: Server,
	}
	for status, want := range cases {
		if got := FromAPIError("SOME_FUTURE_CODE", status); got != want {
			t.Errorf("status %d → %d, want %d", status, got, want)
		}
	}
}

func TestLocalAppExitCodes(t *testing.T) {
	if AppNotRunning != 8 || SandboxBlocked != 9 {
		t.Fatalf("AppNotRunning=%d SandboxBlocked=%d", AppNotRunning, SandboxBlocked)
	}
	seen := map[int]string{}
	for name, v := range map[string]int{"OK": OK, "Usage": Usage, "Auth": Auth, "NotFound": NotFound,
		"Conflict": Conflict, "Server": Server, "Network": Network,
		"AppNotRunning": AppNotRunning, "SandboxBlocked": SandboxBlocked} {
		if other, dup := seen[v]; dup {
			t.Fatalf("%s 与 %s 退出码重复（%d）", name, other, v)
		}
		seen[v] = name
	}
}
