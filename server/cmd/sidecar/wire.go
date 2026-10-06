package main

import (
	"context"
	"database/sql"
	"errors"

	"github.com/james/huabu_studio/apps/api/internal/aicharacters"
	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
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
	// seedance 是 Seedance 合规校验服务，main 用根 ctx 起它的对账 worker（未配置时自身 no-op）。
	seedance *library.SeedanceService
}

// wire 是唯一的装配点（对应参考仓 cmd/server/main.go 里画布相关的那部分）。
// 生成任务清理（taskProjectCleanup）在 P1b 传 nil，P1c 接 generation 后改传 generation 仓库；
// 画布变更广播（SetChangeNotifier）在 P1b 不注入，P1c 接 Agent Hub。
func wire(conn *sql.DB, storage *assets.LocalStorage, auth localauth.Config, dataDir, version string) *app {
	signer := apphttp.NewMediaSigner(storage)
	projectsRepo := projects.NewSQLiteRepository(conn)
	assetsRepo := assets.NewSQLiteRepository(conn)
	usersRepo := users.NewSQLiteRepository(conn)
	orphans := library.NewSQLiteOrphansRepository(conn)
	folders := library.NewSQLiteFoldersRepository(conn)
	items := library.NewSQLiteItemsRepository(conn)
	duplicator := workflows.NewAssetDuplicator(storage, assetsRepo, usersRepo, workflows.NewDeleteNowQueue(storage))
	sweeper := library.NewOrphanSweeper(orphans, storage, conn)

	deps := apphttp.Deps{
		Auth:              auth,
		DataDir:           dataDir,
		Version:           version,
		MediaSigner:       signer,
		Media:             apphttp.NewMediaHandler(storage, signer, apphttp.NewSQLMediaMimeStore(conn), reveal.InFileManager),
		Me:                apphttp.NewMeHandler(usersRepo),
		Asset:             apphttp.NewAssetHandler(storage, assetsRepo, usersRepo, signer),
		Project:           apphttp.NewProjectHandler(projectsRepo, signer, storage, assetsRepo, usersRepo, nil, orphans, conn, duplicator),
		CanvasMutation:    apphttp.NewCanvasMutationHandler(projectsRepo, canvasowner.NewLocal(), signer),
		CanvasAssetImport: apphttp.NewCanvasAssetImportHandler(apphttp.NewSQLCanvasAssetRefStore(conn), assetsRepo),
		LibraryFolder:     apphttp.NewLibraryFolderHandler(folders, items, orphans, usersRepo, conn),
		LibraryItem:       apphttp.NewLibraryItemHandler(items, folders, orphans, storage, usersRepo, signer, conn),
		LibraryBridge:     apphttp.NewLibraryBridgeHandler(items, folders, assetsRepo, signer, conn),
		AdminGC:           apphttp.NewAdminGCHandler(sweeper),
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
	// 生成时自动以 asset://<id> 提交（generationSvc.WithRealPersonResolver）待 P1c 合并后接。
	librarySeedanceSvc.SetPublicAssets(aiCharactersRepo)

	// 生成管线（canvasGenSvc / generationSvc）在 P1c 合并后替换 pendingGeneration。
	gen := pendingGeneration{}
	deps.CanvasPostprocess = apphttp.NewCanvasPostprocessHandler(settingsRepo, gen, projectsRepo, assetsRepo, gen, modelsRepo, signer).
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

	cleaner := apphttp.NewProjectCleaner(projectsRepo, assetsRepo, usersRepo, nil, orphans, conn)
	return &app{deps: deps, cleaner: cleaner, sweeper: sweeper, seedance: librarySeedanceSvc}
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

// errGenerationPending：P1c 生成管线合并前，去字幕/转高清/转白模的提交无处入队。
var errGenerationPending = errors.New("生成管线尚未接入")

// pendingGeneration 在 P1c 合并前占位 canvasgen.Service（Enqueue）与 generation.Service（Get）。
// P1c 合并后在上面直接传 canvasGenSvc、generationSvc，并删掉本类型。
type pendingGeneration struct{}

func (pendingGeneration) Enqueue(context.Context, canvasgen.EnqueueInput) (int64, error) {
	return 0, errGenerationPending
}

func (pendingGeneration) Get(context.Context, int64, int64) (*generation.Task, error) {
	return nil, generation.ErrNotFound
}
