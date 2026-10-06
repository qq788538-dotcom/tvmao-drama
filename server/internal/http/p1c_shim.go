//go:build p1cshim

// 本文件只供 P1c 在 P1b 合并前独立编译、测试（go test -tags p1cshim）。
// 内容逐字取自参考仓 http/context.go 与 http/helpers.go；P1b 合并后默认构建不再包含本文件，
// Task 10 删除它。
package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
)

type contextKey string

const userIDContextKey contextKey = "userID"

func withUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userIDContextKey, userID)
}

func userIDFromContext(ctx context.Context) int64 {
	value, _ := ctx.Value(userIDContextKey).(int64)
	return value
}

func writeError(w nethttp.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func decodeJSON(r *nethttp.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
