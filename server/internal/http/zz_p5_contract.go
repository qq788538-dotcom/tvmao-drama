// 本文件是 P5 的契约桩：P1b/P1c 尚未合并时让 agent 包能编译。内容逐字取自参考仓
// fea92ab 的同名声明。最终合并时，只要该包已有真实实现（go build 报重复声明），就删除本文件。

package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

type contextKey string

const (
	projectIDContextKey contextKey = "projectID"
	userIDContextKey    contextKey = "userID"
)

func withProjectID(ctx context.Context, projectID int64) context.Context {
	return context.WithValue(ctx, projectIDContextKey, projectID)
}

func projectIDFromContext(ctx context.Context) int64 {
	value, _ := ctx.Value(projectIDContextKey).(int64)
	return value
}

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

func ProjectFromURL(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		raw := chi.URLParam(r, "projectID")
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, nethttp.StatusBadRequest, "invalid project id")
			return
		}
		next.ServeHTTP(w, r.WithContext(withProjectID(r.Context(), id)))
	})
}
