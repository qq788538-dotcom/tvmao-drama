// Package http 装配 sidecar 的全部 HTTP 路由。包名和路径与参考仓
// apps/api/internal/http 一致，P1 起业务 handler 直接落在这个包里。
package http

import (
	nethttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

// Deps 是 DI 容器，由 cmd/sidecar/main.go 填充。
type Deps struct {
	Auth    localauth.Config
	DataDir string
	Version string
}

func NewRouter(d Deps) nethttp.Handler {
	r := chi.NewRouter()
	// 鉴权必须挂在最外层：挂在 Group 里的话，预检请求（OPTIONS）打到只注册了
	// GET 的路径会先被 chi 判成 405，根本走不到中间件。
	r.Use(localauth.Middleware(d.Auth))

	r.Get("/api/v1/ping", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true, "version": d.Version})
	})
	r.Get("/api/v1/agent/ws", wsEcho)

	diag := &diagHandler{dir: d.DataDir}
	r.Post("/api/v1/diag/upload", diag.upload)
	r.Get("/api/v1/diag/media/{name}", diag.media)
	return r
}
