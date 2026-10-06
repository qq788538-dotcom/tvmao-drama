// Package http 装配 sidecar 的全部 HTTP 路由。包名和路径与参考仓
// apps/api/internal/http 一致，P1 起业务 handler 直接落在这个包里。
package http

import (
	"encoding/json"
	nethttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

// Deps 是 DI 容器，由 cmd/sidecar/main.go 填充。
type Deps struct {
	Auth    localauth.Config
	DataDir string
	Version string

	// P5 Agent。为 nil 时不注册对应路由（P0 测试与未装配场景）。
	Agent      *AgentHandler
	Skills     *SkillsHandler
	AdminAgent *AdminAgentHandler
}

func NewRouter(d Deps) nethttp.Handler {
	r := chi.NewRouter()
	// 鉴权必须挂在最外层：挂在 Group 里的话，预检请求（OPTIONS）打到只注册了
	// GET 的路径会先被 chi 判成 405，根本走不到中间件。
	r.Use(localauth.Middleware(d.Auth))

	r.Get("/api/v1/ping", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true, "version": d.Version})
	})
	// P0 的 WebSocket 回显诊断移到这里，/api/v1/agent/ws 交给真正的 Agent。
	r.Get("/api/v1/diag/ws", wsEcho)

	if d.Agent != nil {
		r.Get("/api/v1/agent/ws", d.Agent.WS)
		// 逐条注册而不 Mount 子路由：与其他计划的 /api/v1/projects/{projectID} 路由组共存不冲突。
		withProject := r.With(ProjectFromURL)
		withProject.Get("/api/v1/projects/{projectID}/agent/threads", d.Agent.ListThreads)
		withProject.Post("/api/v1/projects/{projectID}/agent/threads", d.Agent.CreateThread)
		withProject.Put("/api/v1/projects/{projectID}/agent/threads/{threadID}", d.Agent.UpdateThread)
		withProject.Delete("/api/v1/projects/{projectID}/agent/threads/{threadID}", d.Agent.DeleteThread)
		withProject.Get("/api/v1/projects/{projectID}/agent/threads/{threadID}/messages", d.Agent.ListMessages)
	}
	if d.Skills != nil {
		r.Get("/api/v1/skills", d.Skills.List)
		r.Get("/api/v1/skills/categories", d.Skills.Categories)
		r.Get("/api/v1/skills/featured", d.Skills.Featured)
	}
	if d.AdminAgent != nil {
		r.Get("/api/v1/admin/agent/backbone-models", d.AdminAgent.List)
		r.Put("/api/v1/admin/agent/backbone-models/{id}", d.AdminAgent.Update)
		r.Post("/api/v1/admin/agent/backbone-models/{id}/reset", d.AdminAgent.Reset)
	}

	diag := &diagHandler{dir: d.DataDir}
	r.Post("/api/v1/diag/upload", diag.upload)
	r.Get("/api/v1/diag/media/{name}", diag.media)
	return r
}

func writeJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
