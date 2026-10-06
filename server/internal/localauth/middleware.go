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
	// MediaToken 是只放行 GET/HEAD /media/* 的第二个 token，由 sidecar 每次启动自己生成，
	// 拼进发给前端的媒体 URL（<img>/<video> 带不了请求头）。媒体 URL 会出现在 DOM、日志、
	// 交给 Agent 的提示词里，单独一个 token 让它泄露也只能读媒体、不能调接口。为空时不启用。
	MediaToken string
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
			// 无论请求带不带 Origin 都回 Vary: Origin：响应是否带 ACAO 取决于 Origin，
			// 否则不带 Origin 的 <img> 会让缓存存下无 ACAO 的响应，复用给之后的 CORS 请求。
			w.Header().Add("Vary", "Origin")
			// 带 Origin 的请求来自浏览器上下文；不在白名单的一律挡掉，哪怕 token 对。
			if origin := r.Header.Get("Origin"); origin != "" {
				if !allowed[origin] {
					http.Error(w, "origin not allowed", http.StatusForbidden)
					return
				}
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Range")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")
				// 预检结果缓存 10 分钟：画布拖动时每个 PATCH 都先发一次预检，不缓存会翻倍请求数。
				h.Set("Access-Control-Max-Age", "600")
			}
			// 真正的 CORS 预检请求：OPTIONS + Origin 白名单内 + Access-Control-Request-Method。
			// 只这种情况下浏览器不会带自定义头，token 校验留给随后的真实请求。
			// 裸 OPTIONS 请求（无 Origin 或无 Access-Control-Request-Method）必须带 token。
			if r.Method == http.MethodOptions && r.Header.Get("Origin") != "" && r.Header.Get("Access-Control-Request-Method") != "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if !tokenOK(r, cfg) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// tokenOK 先看 Authorization: Bearer，没有（或是别的认证方案）再看 ?token=。后者给 <img>/<video>
// 和 WebSocket 握手用——它们带不了自定义头。媒体 token 只对 GET/HEAD /media/* 有效。
func tokenOK(r *http.Request, cfg Config) bool {
	if cfg.Token == "" {
		return false
	}
	got := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	}
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	if equal(got, cfg.Token) {
		return true
	}
	return cfg.MediaToken != "" && isMediaRead(r) && equal(got, cfg.MediaToken)
}

func equal(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func isMediaRead(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasPrefix(r.URL.Path, "/media/")
}
