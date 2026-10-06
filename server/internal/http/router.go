// Package http 装配 sidecar 的全部 HTTP 路由。包名和路径与参考仓
// apps/api/internal/http 一致，业务 handler 直接落在这个包里。
package http

import (
	nethttp "net/http"
	"strings"

	"github.com/go-chi/chi/v5"

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
	r.Get("/media/*", d.Media.Serve)
	r.Head("/media/*", d.Media.Serve)

	// P1c 生成链路：registerGenerationRoutes / registerRepersistRoute 注册的是 /api/v1/... 绝对路径，
	// 所以挂在根路由的 Group 上（与下面 /api/v1 子路由里的业务路由同样套 LocalIdentity）。
	// chi 先匹配静态段，这些路径不会被 /api/v1 子路由的通配吞掉。
	r.Group(func(r chi.Router) {
		r.Use(LocalIdentity)
		registerGenerationRoutes(r, d.Generation)
		registerRepersistRoute(r, d.GenerationRepersist)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/ping", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true, "version": d.Version})
		})
		// P0 的回显占位；P1c 换成只做 canvas_changed 的最小 Agent hub，P5 换成完整 Agent。
		r.Get("/agent/ws", wsEcho)
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

			// 手动触发孤儿清扫（sidecar 也会在启动时和每天自动跑）。
			r.Post("/admin/gc", d.AdminGC.Run)
		})
	})
	return r
}
