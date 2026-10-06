package http

import "github.com/go-chi/chi/v5"

// GenerationRoutes 是 P1c 生成链路的 HTTP 入口集合，由 cmd/sidecar 的 wireGeneration 构造。
// nil 字段对应的路由不注册（测试、或依赖未装配时）。
type GenerationRoutes struct {
	CanvasGen  *CanvasGenHandler
	Generation *GenerationHandler
	Apply      *GenerationApplyHandler
	Models     *RuntimeModelHandler
}

// registerGenerationRoutes 注册绝对路径，调用方负责把它挂在带本地身份中间件（userID=1）的路由上。
// 路径与参考仓 router.go:255-259、638-647 一致；desub/upscale/whitemodel/postprocess-estimate 属 P1d。
func registerGenerationRoutes(r chi.Router, h GenerationRoutes) {
	if h.Models != nil {
		r.Get("/api/v1/models", h.Models.List)
		r.Post("/api/v1/models/estimate", h.Models.Estimate)
		r.Post("/api/v1/models/estimate-batch", h.Models.EstimateBatch)
	}
	if h.Generation != nil {
		r.Get("/api/v1/tasks", h.Generation.List)
		r.Delete("/api/v1/tasks/{taskID}", h.Generation.Delete)
	}
	if h.Apply != nil {
		r.Post("/api/v1/tasks/{taskID}/apply-to-canvas", h.Apply.ApplyToCanvas)
	}
	if h.CanvasGen != nil {
		r.Post("/api/v1/canvas-tasks", h.CanvasGen.Submit)
		r.Get("/api/v1/canvas-tasks/by-node", h.CanvasGen.NodeStatus)
		r.Get("/api/v1/canvas-tasks/{taskID}", h.CanvasGen.Status)
	}
}
