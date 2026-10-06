// Package http 装配 sidecar 的全部 HTTP 路由。包名和路径与参考仓
// apps/api/internal/http 一致，业务 handler 直接落在这个包里。
package http

import (
	nethttp "net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/aicharacters"
	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

// Deps 是 DI 容器，由 cmd/sidecar 的 wire 填充。
type Deps struct {
	Auth    localauth.Config
	DataDir string
	Version string

	MediaSigner       *MediaSigner
	Media             *MediaHandler
	Me                *MeHandler
	Asset             *AssetHandler
	Project           *ProjectHandler
	CanvasMutation    *CanvasMutationHandler
	CanvasAssetImport *CanvasAssetImportHandler
	LibraryFolder     *LibraryFolderHandler
	LibraryItem       *LibraryItemHandler
	LibraryBridge     *LibraryBridgeHandler
	AdminGC           *AdminGCHandler

	// Generation 是 P1c 生成链路的 HTTP 入口（nil 字段不注册对应路由）。
	Generation GenerationRoutes
	// GenerationRepersist 是节点「未保存到本地」的重试接口（spec S5）。
	GenerationRepersist *GenerationRepersistHandler
	// ── P1d ──
	CanvasPostprocess *CanvasPostprocessHandler
	GenImagePreset    *GenImagePresetHandler
	MultiAngle        *MultiAngleHandler
	LightControl      *LightControlHandler
	AICharacter       *AICharacterHandler
	// AICharacterImages 包在 /media/* 外面：角色形象图本地没有时先下载缓存（spec F7）。nil = 不包。
	AICharacterImages *aicharacters.ImageCache
	Seedance          *SeedanceHandler
	DirectorModel     *DirectorModelHandler
	DirectorLibrary   *DirectorLibraryHandler

	// ── P5 Agent ── 为 nil 时不注册对应路由（P0 路由测试与 wire() 的 e2e 不装配 Agent）。
	// sidecar 的 main 总会装配（wireAgent 需要根 ctx，所以不在 wire() 里）。
	Agent      *AgentHandler
	Skills     *SkillsHandler
	AdminAgent *AdminAgentHandler

	// CanvasScript 脚本节点接口（P6）；nil 时不注册，便于只测其他路由。
	CanvasScript *CanvasScriptHandler
	// SettingsCenter 是设置页接口（P4，/api/v1/settings/*）的依赖；为 nil 的那部分接口不注册。
	SettingsCenter SettingsCenterDeps
}

// MustWire 在启动期校验业务 handler 都已装配，缺一个就 panic。路由按字段注册，漏装配不会编译失败，
// 直到有请求进来才 nil 解引用（参考仓 MustWireCLI 的同一思路）。不放进 NewRouter：P0 的路由测试
// 只装配了 ping/diag，放进去会把它们打挂。
func MustWire(d Deps) {
	var missing []string
	for name, ok := range map[string]bool{
		"MediaSigner": d.MediaSigner != nil, "Media": d.Media != nil, "Me": d.Me != nil, "Asset": d.Asset != nil,
		"Project": d.Project != nil, "CanvasMutation": d.CanvasMutation != nil, "CanvasAssetImport": d.CanvasAssetImport != nil,
		"LibraryFolder": d.LibraryFolder != nil, "LibraryItem": d.LibraryItem != nil, "LibraryBridge": d.LibraryBridge != nil,
		"AdminGC":              d.AdminGC != nil,
		"Generation.CanvasGen": d.Generation.CanvasGen != nil, "Generation.Generation": d.Generation.Generation != nil,
		"Generation.Apply": d.Generation.Apply != nil, "Generation.Models": d.Generation.Models != nil,
		"GenerationRepersist": d.GenerationRepersist != nil,
		"CanvasPostprocess":   d.CanvasPostprocess != nil, "GenImagePreset": d.GenImagePreset != nil,
		"MultiAngle": d.MultiAngle != nil, "LightControl": d.LightControl != nil, "AICharacter": d.AICharacter != nil,
		"Seedance": d.Seedance != nil, "DirectorModel": d.DirectorModel != nil, "DirectorLibrary": d.DirectorLibrary != nil,
	} {
		if !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		panic("sidecar 依赖未接线（检查 cmd/sidecar/wire.go）: " + strings.Join(missing, ", "))
	}
}

func NewRouter(d Deps) nethttp.Handler {
	r := chi.NewRouter()
	// 鉴权必须挂在最外层：挂在 Group 里的话，预检请求（OPTIONS）打到只注册了
	// GET 的路径会先被 chi 判成 405，根本走不到中间件。
	r.Use(localauth.Middleware(d.Auth))

	// 本地媒体文件：<img>/<video> 直接请求，token 走 ?token=（媒体专用 token 只能访问这里）。
	// 角色形象图本地没有时先下载缓存再交给 Media.Serve（spec F7）。
	mediaMW := func(next nethttp.Handler) nethttp.Handler {
		if d.AICharacterImages == nil {
			return next
		}
		return d.AICharacterImages.Middleware(next)
	}
	r.With(mediaMW).Get("/media/*", d.Media.Serve)
	r.With(mediaMW).Head("/media/*", d.Media.Serve)

	// P1c 生成链路：registerGenerationRoutes / registerRepersistRoute 注册的是 /api/v1/... 绝对路径，
	// 所以挂在根路由的 Group 上（与下面 /api/v1 子路由里的业务路由同样套 LocalIdentity）。
	// chi 先匹配静态段，这些路径不会被 /api/v1 子路由的通配吞掉。
	r.Group(func(r chi.Router) {
		r.Use(LocalIdentity)
		registerGenerationRoutes(r, d.Generation)
		registerRepersistRoute(r, d.GenerationRepersist)
	})

	// P4 设置页：mountSettingsCenter 注册的是 /api/v1/settings 绝对前缀，同样挂在根路由的 LocalIdentity 组上。
	// DataDir / Version 缺省时取 sidecar 自身的值。
	sc := d.SettingsCenter
	if sc.DataDir == "" {
		sc.DataDir = d.DataDir
	}
	if sc.Version == "" {
		sc.Version = d.Version
	}
	r.Group(func(r chi.Router) {
		r.Use(LocalIdentity)
		mountSettingsCenter(r, sc)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/ping", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true, "version": d.Version})
		})
		// P0 的 WebSocket 回显诊断移到 /diag/ws，/api/v1/agent/ws 交给 P5 的 Agent（见下方 LocalIdentity 组）。
		r.Get("/diag/ws", wsEcho)
		// 诊断页（P3 移进“设置 → 环境诊断”）。
		diag := &diagHandler{dir: d.DataDir}
		r.Post("/diag/upload", diag.upload)
		r.Get("/diag/media/{name}", diag.media)

		r.Group(func(r chi.Router) {
			r.Use(LocalIdentity)

			r.Get("/me", d.Me.Me)

			r.Get("/projects", d.Project.List)
			r.Post("/projects", d.Project.Create)
			r.Route("/projects/{projectID}", func(r chi.Router) {
				r.Use(ProjectFromURL)
				r.Get("/", d.Project.Get)
				r.Put("/", d.Project.Update)
				r.Delete("/", d.Project.Delete)
				r.Get("/snapshot", d.Project.GetSnapshot)
				r.Get("/images", d.Project.ListImages)
				r.Put("/cover", d.Project.SetCover)
				r.Post("/duplicate", d.Project.Duplicate)
				// 「复制节点」跨项目粘贴后的资产引用登记。
				r.Post("/assets/import-refs", d.CanvasAssetImport.ImportRefs)
				// 编辑锁三个端点保留（前端协议不变），本地实现恒放行（spec X2、S11）。
				r.Get("/canvas-owner/probe", d.CanvasMutation.ProbeOwner)
				r.Post("/canvas/owner/claim", d.CanvasMutation.ClaimOwner)
				r.Post("/canvas/owner/heartbeat", d.CanvasMutation.HeartbeatOwner)
				r.Post("/canvas/nodes", d.CanvasMutation.UpsertNode)
				r.Patch("/canvas/nodes/positions", d.CanvasMutation.PatchNodePositions)
				r.Patch("/canvas/nodes/{nodeID}", d.CanvasMutation.PatchNode)
				r.Delete("/canvas/nodes", d.CanvasMutation.DeleteNodes)
				r.Post("/canvas/connections", d.CanvasMutation.UpsertConnection)
				r.Delete("/canvas/connections/{connectionID}", d.CanvasMutation.DeleteConnection)
			})

			r.Post("/assets", d.Asset.Upload)
			r.Get("/assets", d.Asset.List)

			// 画布展示层批量换缩略图 URL；本地不生成缩略图，返回原图 URL（spec S8）。
			r.Post("/media/thumbs", MediaThumbs(d.MediaSigner))
			// 节点右键「在访达/资源管理器中显示」（spec S3）。
			r.Post("/media/reveal", d.Media.Reveal)

			r.Route("/library", func(r chi.Router) {
				r.Get("/folders", d.LibraryFolder.List)
				r.Post("/folders", d.LibraryFolder.Create)
				r.Patch("/folders/{folderID}", d.LibraryFolder.Update)
				r.Delete("/folders/{folderID}", d.LibraryFolder.Delete)
				r.Post("/folders/{folderID}/items/upload", d.LibraryItem.Upload)
				r.Get("/items", d.LibraryItem.List)
				r.Patch("/items/{itemID}", d.LibraryItem.Update)
				r.Delete("/items/{itemID}", d.LibraryItem.Delete)
				r.Post("/items/{itemID}/apply-to-canvas", d.LibraryBridge.ApplyToCanvas)
			})
			r.Post("/projects/{projectID}/assets/{assetID}/save-to-library", d.LibraryBridge.SaveToLibrary)
			r.Post("/library/save-from-url", d.LibraryBridge.SaveFromURL)

			// ── P1d：后处理、预设、多角度、打光 ──
			r.Get("/canvas/postprocess-config", d.CanvasPostprocess.GetConfig)
			r.Get("/canvas/gen-image-presets", d.GenImagePreset.UserGet)
			r.Get("/canvas/multi-angle", d.MultiAngle.UserGet)
			r.Get("/canvas/light-control", d.LightControl.UserGet)
			r.Post("/canvas-tasks/desub", d.CanvasPostprocess.Desub)
			r.Post("/canvas-tasks/postprocess-estimate", d.CanvasPostprocess.Estimate)
			r.Post("/canvas-tasks/upscale", d.CanvasPostprocess.Upscale)
			r.Post("/canvas-tasks/whitemodel", d.CanvasPostprocess.WhiteModel)
			// ── P1d：AI 角色库、Seedance 合规 ──
			r.Get("/canvas/ai-characters", d.AICharacter.List)
			r.Post("/seedance/verify", d.Seedance.Verify)
			r.Post("/seedance/status", d.Seedance.Status)
			r.Get("/seedance/config", d.Seedance.Config)
			// ── P1d：3D 导演台 ──
			r.Post("/director/models", d.DirectorModel.Upload)
			r.Get("/director/models", d.DirectorModel.List)
			r.Delete("/director/models/{modelID}", d.DirectorModel.Delete)
			r.Get("/director/library", d.DirectorLibrary.List)
			// /admin/canvas/* 不在这里注册（P4）。

			// 手动触发孤儿清扫（sidecar 也会在启动时和每天自动跑）。
			r.Post("/admin/gc", d.AdminGC.Run)

			// ── P5：Agent、Skill、backbone 管理 ──
			if d.Agent != nil {
				r.Get("/agent/ws", d.Agent.WS)
				r.With(ProjectFromURL).Get("/projects/{projectID}/agent/threads", d.Agent.ListThreads)
				r.With(ProjectFromURL).Post("/projects/{projectID}/agent/threads", d.Agent.CreateThread)
				r.With(ProjectFromURL).Put("/projects/{projectID}/agent/threads/{threadID}", d.Agent.UpdateThread)
				r.With(ProjectFromURL).Delete("/projects/{projectID}/agent/threads/{threadID}", d.Agent.DeleteThread)
				r.With(ProjectFromURL).Get("/projects/{projectID}/agent/threads/{threadID}/messages", d.Agent.ListMessages)
			}
			if d.Skills != nil {
				r.Get("/skills", d.Skills.List)
				r.Get("/skills/categories", d.Skills.Categories)
				r.Get("/skills/featured", d.Skills.Featured)
			}
			if d.AdminAgent != nil {
				r.Get("/admin/agent/backbone-models", d.AdminAgent.List)
				r.Put("/admin/agent/backbone-models/{id}", d.AdminAgent.Update)
				r.Post("/admin/agent/backbone-models/{id}/reset", d.AdminAgent.Reset)
			}

			// ── P6：脚本节点 ──
			if d.CanvasScript != nil {
				r.Get("/canvas-script/styles", d.CanvasScript.Styles)
				r.Post("/canvas-script-tasks", d.CanvasScript.Submit)
				r.Get("/canvas-script-tasks/{taskID}", d.CanvasScript.Status)
			}
		})
	})
	return r
}
