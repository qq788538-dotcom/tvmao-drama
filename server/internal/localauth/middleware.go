// Package localauth 是桌面端 sidecar 的本地鉴权：每次启动一个随机 token，
// 外加 Origin 白名单。sidecar 只监听 127.0.0.1，但本机浏览器里任意网页都能
// 向这个端口发请求，所以两道都不能省（spec SEC1、SEC2）。
package localauth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

type Config struct {
	// Token 由 Tauri 壳每次启动生成，经 stdin 传入。为空时拒绝一切请求。
	Token string
	// AllowedOrigins 是允许的 Origin 全量白名单（完整匹配）。
	AllowedOrigins []string
}

func Middleware(cfg Config) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowed[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 带 Origin 的请求来自浏览器上下文；不在白名单的一律挡掉，哪怕 token 对。
			if origin := r.Header.Get("Origin"); origin != "" {
				if !allowed[origin] {
					http.Error(w, "origin not allowed", http.StatusForbidden)
					return
				}
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Vary", "Origin")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Range")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")
			}
			// 真正的 CORS 预检请求：OPTIONS + Origin 白名单内 + Access-Control-Request-Method。
			// 只这种情况下浏览器不会带自定义头，token 校验留给随后的真实请求。
			// 裸 OPTIONS 请求（无 Origin 或无 Access-Control-Request-Method）必须带 token。
			if r.Method == http.MethodOptions && r.Header.Get("Origin") != "" && r.Header.Get("Access-Control-Request-Method") != "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if !tokenOK(r, cfg.Token) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// tokenOK 先看 Authorization 头，没有再看 ?token=。后者给 <img>/<video> 和
// WebSocket 握手用——它们带不了自定义头。
func tokenOK(r *http.Request, want string) bool {
	if want == "" {
		return false
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
