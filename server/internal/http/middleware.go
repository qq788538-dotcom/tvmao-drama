package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// LocalUserID 是桌面端唯一的本地用户（spec X4；迁移 0001 预置 users.id=1，role=admin）。
const LocalUserID int64 = 1

// LocalIdentity 取代参考仓的 AuthMiddleware.Require：桌面端没有登录，请求能走到这里说明
// 已经过了 localauth 的 token + Origin 校验，直接注入本地用户身份。
// 不写 actor——走个人路径，handler 里的团队分支全部不可达（spec X2）。
func LocalIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withUserID(r.Context(), LocalUserID)
		ctx = withUserRole(ctx, "admin")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func ProjectFromURL(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := chi.URLParam(r, "projectID")
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid project id")
			return
		}
		next.ServeHTTP(w, r.WithContext(withProjectID(r.Context(), id)))
	})
}
