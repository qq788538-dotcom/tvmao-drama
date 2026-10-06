package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/james/huabu_studio/apps/api/internal/aicharacters"
	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/canvasowner"
	"github.com/james/huabu_studio/apps/api/internal/directorlibrary"
	"github.com/james/huabu_studio/apps/api/internal/directormodels"
	"github.com/james/huabu_studio/apps/api/internal/generation"
	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/localauth"
	"github.com/james/huabu_studio/apps/api/internal/models"
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
	// seedance 是 Seedance 合规校验服务，main 用根 ctx 起它的对账 worker（未配置时自身 no-op）。
	seedance *library.SeedanceService
}

// wire 是唯一的装配点（对应参考仓 cmd/server/main.go 里画布相关的那部分）。
// 生成任务清理（taskProjectCleanup）接 generation 仓库：删项目时清掉该项目的终态任务历史。
// 画布变更广播（canvas_changed）目前没有 Hub（/agent/ws 仍是 P0 回显占位），Notifier 传 nil：
// 回写照常、不广播；P5 的 Agent Hub 落地后在这里注入。
// 生成后台循环不在这里启动，由 run() 在 HTTP 就绪前用可取消的 ctx 调 a.gen.Gen.StartBackground。
func wire(conn *sql.DB, storage *assets.LocalStorage, auth localauth.Config, dataDir, version string) (*app, error) {
	signer := apphttp.NewMediaSigner(storage)
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

	deps := apphttp.Deps{
		Auth:              auth,
		DataDir:           dataDir,
		Version:           version,
		MediaSigner:       signer,
		Media:             apphttp.NewMediaHandler(storage, signer, apphttp.NewSQLMediaMimeStore(conn), reveal.InFileManager),
		Me:                apphttp.NewMeHandler(usersRepo),
		Asset:             apphttp.NewAssetHandler(storage, assetsRepo, usersRepo, signer),
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

	// ── P1d ──
	settingsRepo := settings.NewSQLiteRepository(conn)
	modelsRepo := models.NewSQLiteRepository(conn)
	aiCharactersRepo := aicharacters.NewSQLiteRepository(conn)
	charImages := aicharacters.NewImageCache(aiCharactersRepo, settingsRepo.Get, charImageStore{storage})

	// Seedance 合规走 newapi（spec X5）：地址、路径每次请求现读 settings。
	seedanceClient := seedance.NewClient(
		func() string { v, _ := settingsRepo.Get("base_domain"); return v },
		func() seedance.Paths { c, _ := seedance.LoadComplianceConfig(settingsRepo.Get); return c.Paths },
	)
	librarySeedanceSvc := library.NewSeedanceService(
		library.NewSQLiteSeedanceAssetsRepository(conn), library.NewSQLiteSeedanceGroupStore(conn),
		seedanceClient, settingsRepo, signer)
	// AI 角色图是平台预置、已过校验的公共资产：状态角标兜底。
	librarySeedanceSvc.SetPublicAssets(aiCharactersRepo)
	// 生成时真人素材自动以 asset://<id> 提交（参考仓 main.go 同名装配）。
	genW.Gen.WithRealPersonResolver(librarySeedanceSvc)

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

	cleaner := apphttp.NewProjectCleaner(projectsRepo, assetsRepo, usersRepo, genTasks, orphans, conn)
	return &app{deps: deps, cleaner: cleaner, sweeper: sweeper, gen: genW, seedance: librarySeedanceSvc}, nil
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
