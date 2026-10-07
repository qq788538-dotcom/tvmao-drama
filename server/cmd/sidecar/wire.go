package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/james/huabu_studio/apps/api/internal/agent"
	"github.com/james/huabu_studio/apps/api/internal/aicharacters"
	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/builtin"
	"github.com/james/huabu_studio/apps/api/internal/canvasowner"
	"github.com/james/huabu_studio/apps/api/internal/cliapi"
	"github.com/james/huabu_studio/apps/api/internal/directorlibrary"
	"github.com/james/huabu_studio/apps/api/internal/directormodels"
	"github.com/james/huabu_studio/apps/api/internal/events"
	"github.com/james/huabu_studio/apps/api/internal/generation"
	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/localauth"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/objectstore"
	"github.com/james/huabu_studio/apps/api/internal/projects"
	"github.com/james/huabu_studio/apps/api/internal/reveal"
	"github.com/james/huabu_studio/apps/api/internal/seedance"
	"github.com/james/huabu_studio/apps/api/internal/settings"
	"github.com/james/huabu_studio/apps/api/internal/users"
	"github.com/james/huabu_studio/apps/api/internal/workflows"
)

// app 是装配好的 sidecar：HTTP 依赖 + 需要在启动后运行的后台任务。
type app struct {
	deps    apphttp.Deps
	cleaner *apphttp.ProjectCleaner
	sweeper *library.OrphanSweeper
	gen     *genWiring
	// assets 交给 main 里的 wireAgent（附件查找）。
	assets *assets.SQLiteRepository
	// seedance 是 Seedance 合规校验服务，main 用根 ctx 起它的对账 worker（未配置时自身 no-op）。
	seedance *library.SeedanceService
	// publisher 是对象存储发布器（用户自配 TOS/OSS），main 把它交给 wireAgent 的媒体解析。
	publisher *objectstore.Publisher
	// events 是项目变更事件中心：CLI 写画布后发布，网页经 SSE 订阅；退出时由 main 关闭。
	events *events.Hub
}

// wire 是唯一的装配点（对应参考仓 cmd/server/main.go 里画布相关的那部分）。
// 生成任务清理（taskProjectCleanup）接 generation 仓库：删项目时清掉该项目的终态任务历史。
// 画布变更广播（canvas_changed）依赖 P5 的 Agent Hub，Hub 在 run() 里 wireAgent 之后才存在：
// 这里构造的 handler 与生成回写都先不带 Notifier，由 run() 用 SetChangeNotifier 补注入。
// 生成后台循环不在这里启动，由 run() 在 HTTP 就绪前用可取消的 ctx 调 a.gen.Gen.StartBackground。
func wire(conn *sql.DB, storage *assets.LocalStorage, auth localauth.Config, dataDir, version string) (*app, error) {
	signer := apphttp.NewMediaSigner(storage)
	// 对象存储（用户自配 TOS/OSS）：本机视频/音频经它交给上游（对象存储设计 §3.2）。
	publisher := objectstore.NewPublisher(settings.NewSQLiteRepository(conn), storage, nil)
	projectsRepo := projects.NewSQLiteRepository(conn)
	assetsRepo := assets.NewSQLiteRepository(conn)
	usersRepo := users.NewSQLiteRepository(conn)
	orphans := library.NewSQLiteOrphansRepository(conn)
	folders := library.NewSQLiteFoldersRepository(conn)
	items := library.NewSQLiteItemsRepository(conn)
	duplicator := workflows.NewAssetDuplicator(storage, assetsRepo, usersRepo, workflows.NewDeleteNowQueue(storage))
	sweeper := library.NewOrphanSweeper(orphans, storage, conn)
	genTasks := generation.NewSQLiteRepository(conn)

	genW, err := wireGeneration(conn, dataDir, genExternal{
		Storage:   storage,
		Assets:    assetsRepo,
		Reclaimer: libraryReclaimer{orphans: orphans},
		Signer:    signer,
	})
	if err != nil {
		return nil, fmt.Errorf("装配生成链路: %w", err)
	}

	// 网页上传与 CLI 上传共用同一个 AssetHandler（CLIAssetHandler 复用它的落盘与登记）。
	assetHandler := apphttp.NewAssetHandler(storage, assetsRepo, usersRepo, signer)

	deps := apphttp.Deps{
		Auth:              auth,
		DataDir:           dataDir,
		Version:           version,
		MediaSigner:       signer,
		Media:             apphttp.NewMediaHandler(storage, signer, apphttp.NewSQLMediaMimeStore(conn), reveal.InFileManager),
		Me:                apphttp.NewMeHandler(usersRepo),
		Asset:             assetHandler,
		Project:           apphttp.NewProjectHandler(projectsRepo, signer, storage, assetsRepo, usersRepo, genTasks, orphans, conn, duplicator),
		CanvasMutation:    apphttp.NewCanvasMutationHandler(projectsRepo, canvasowner.NewLocal(), signer),
		CanvasAssetImport: apphttp.NewCanvasAssetImportHandler(apphttp.NewSQLCanvasAssetRefStore(conn), assetsRepo),
		LibraryFolder:     apphttp.NewLibraryFolderHandler(folders, items, orphans, usersRepo, conn),
		LibraryItem:       apphttp.NewLibraryItemHandler(items, folders, orphans, storage, usersRepo, signer, conn),
		LibraryBridge:     apphttp.NewLibraryBridgeHandler(items, folders, assetsRepo, signer, conn),
		AdminGC:           apphttp.NewAdminGCHandler(sweeper),

		Generation:          genW.Routes,
		GenerationRepersist: genW.Repersist,
	}

	genW.Gen.SetMediaPublisher(publisher)

	// ── P1d ──
	settingsRepo := settings.NewSQLiteRepository(conn)
	modelsRepo := models.NewSQLiteRepository(conn)
	aiCharactersRepo := aicharacters.NewSQLiteRepository(conn)
	charImages := aicharacters.NewImageCache(aiCharactersRepo, settingsRepo.Get, charImageStore{storage})

	// Seedance 合规走 newapi（spec X5）：地址、路径每次请求现读 settings。
	seedanceClient := seedance.NewClient(
		// 合规校验不绑定模型：ResolveUpstream(nil, …) = 全局 base_domain。
		func() string { v, _, _ := models.ResolveUpstream(nil, settingsRepo); return v },
		func() seedance.Paths { c, _ := seedance.LoadComplianceConfig(settingsRepo.Get); return c.Paths },
	)
	librarySeedanceSvc := library.NewSeedanceService(
		library.NewSQLiteSeedanceAssetsRepository(conn), library.NewSQLiteSeedanceGroupStore(conn),
		seedanceClient, settingsRepo, signer)
	// AI 角色图是平台预置、已过校验的公共资产：状态角标兜底。
	librarySeedanceSvc.SetPublicAssets(aiCharactersRepo)
	librarySeedanceSvc.SetMediaPublisher(publisher)
	// 生成时真人素材自动以 asset://<id> 提交（参考仓 main.go 同名装配）。
	genW.Gen.WithRealPersonResolver(librarySeedanceSvc)

	// 自由画布 CLI 契约（参考仓 cmd/server/main.go 同名装配）：service 层及以下与网页共用。
	// CLI 写画布成功后经事件中心通知网页（SSE），网页空闲时整拉快照。
	hub := events.NewHub()
	deps.Events = hub
	cliStore := cliapi.WithChangeNotifier(projectsRepo, hub)
	deps.CLI = apphttp.NewCLIHandler(
		cliapi.NewService(cliStore, signer.Sign).
			WithGeneration(genW.CanvasGen, modelsRepo).
			// 合规校验复用网页那一个 library.SeedanceService：状态挂在 object_key 上，两条路共用一份。
			WithSeedance(librarySeedanceSvc, storage),
		cliapi.NewSQLiteIdempotency(conn),
	)
	deps.CLIModel = apphttp.NewCLIModelHandler(modelsRepo)
	deps.CLIAsset = apphttp.NewCLIAssetHandler(assetHandler, assetsRepo, signer)

	// 后处理（去字幕/转高清/转白模）直接入 P1c 的生成管线。
	deps.CanvasPostprocess = apphttp.NewCanvasPostprocessHandler(settingsRepo, genW.CanvasGen, projectsRepo, assetsRepo, genW.Gen, modelsRepo, signer).
		WithObjectStore(storage)
	deps.GenImagePreset = apphttp.NewGenImagePresetHandler(settingsRepo)
	deps.MultiAngle = apphttp.NewMultiAngleHandler(settingsRepo)
	deps.LightControl = apphttp.NewLightControlHandler(settingsRepo)
	deps.AICharacter = apphttp.NewAICharacterHandler(aiCharactersRepo, signer, settingsRepo.Get)
	deps.AICharacterImages = charImages
	deps.Seedance = apphttp.NewSeedanceHandler(librarySeedanceSvc, storage, conn)
	deps.DirectorModel = apphttp.NewDirectorModelHandler(
		&directormodels.Service{Repo: directormodels.NewSQLiteRepository(conn)},
		storage, orphans, usersRepo, signer, conn)
	deps.DirectorLibrary = apphttp.NewDirectorLibraryHandler(directorlibrary.NewSQLiteRepository(conn), signer)

	// ── P4 设置页 ── 内置模型的修改只经 builtin.Service（白名单 + 改动记录，spec M10/M11）。
	// 五项画布配置与 Agent 模型 2026-10-07 起写死为内置值，设置页不再提供修改接口。/admin/canvas/* 的整份覆盖写接口不注册（见 router.go）。
	policy, err := builtin.NewService(conn)
	if err != nil {
		return nil, fmt.Errorf("初始化内置数据编辑服务: %w", err)
	}
	deps.SettingsCenter = apphttp.SettingsCenterDeps{
		Settings: settingsRepo,
		Models:   modelsRepo,
		// 对象存储连接测试。
		ObjectStore: publisher,
		// 每模型独立上游：本机字段，直接写 model_configs，不经内置编辑策略（2026-10-07）。
		ModelUpstream: modelsRepo,
		ModelEdit:     apphttp.NewPolicyModelEdit(policy, modelsRepo),
		SettingEdit:   apphttp.NewPolicySettingEdit(policy, settingsRepo),
		// Agent 模型只读（2026-10-07 写死），仅供模型中心标注引用。
		Agent: apphttp.NewBackboneAgentModels(agent.NewBackboneRepo(conn)),
	}

	cleaner := apphttp.NewProjectCleaner(projectsRepo, assetsRepo, usersRepo, genTasks, orphans, conn)
	return &app{deps: deps, cleaner: cleaner, sweeper: sweeper, gen: genW, assets: assetsRepo, seedance: librarySeedanceSvc, publisher: publisher, events: hub}, nil
}

// charImageStore 把 assets.Storage 适配成 aicharacters.ObjectStore（角色形象图缓存用）。
type charImageStore struct{ s assets.Storage }

func (c charImageStore) Exists(ctx context.Context, key string) bool {
	rc, err := c.s.GetObject(ctx, key)
	if err != nil {
		return false
	}
	rc.Close()
	return true
}

func (c charImageStore) Put(ctx context.Context, key, contentType string, body []byte) error {
	_, err := c.s.PutObject(ctx, assets.PutObjectInput{Key: key, ContentType: contentType, Body: body})
	return err
}
