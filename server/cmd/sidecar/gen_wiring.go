package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/billing"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasgen"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasstate"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
	apphttp "github.com/qq788538-dotcom/tvmao-drama/server/internal/http"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/projects"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/settings"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/upstream"
)

// 生成链路的装配。

const (
	genLeaseHeartbeatSec = 15                // 租约心跳间隔（秒）
	genLeaseStaleSec     = 90                // 租约过期判定（秒）
	genUpstreamTimeout   = 300 * time.Second // 上游请求超时

	// 产物下载的单文件上限与全局并发闸（generation/download.go）。桌面端目前没有对应的
	// 设置项，与上面的租约参数一样在装配处定死；值与 generation 内的默认值一致，写在这里
	// 是为了让装配处看得到、要调时只改这一处（以后接设置中心也从这里换来源）。
	genMaxImageOutputBytes int64 = 50 << 20 // 图片产物上限 50MB
	genMaxMediaOutputBytes int64 = 1 << 30  // 音视频产物上限 1GB
	genDownloadConcurrency       = 4        // 同时落盘下载的产物数（首搬、续跑、补搬共用）
)

// genCanvasNotifier 与 canvasgen 未导出的 canvasChangeNotifier 同形。
type genCanvasNotifier interface {
	NotifyCanvasChanged(userID, projectID, version int64, sourceMark string, op *canvasstate.CanvasOpSummary)
}

// genMediaSigner 与 http 的 mediaRefSigner、canvasgen 的 mediaSigner 同形；*apphttp.MediaSigner 满足。
type genMediaSigner interface {
	Sign(s string) string
	SignJSON(raw json.RawMessage) json.RawMessage
	Canonicalize(s string) string
}

// genExternal 是生成链路的外部依赖。字段可为 nil：Storage 为 nil 时产物不落本地；
// Assets 为 nil 时不建 asset 行、不注册 apply-to-canvas；Reclaimer 为 nil 时删任务不登记孤儿；
// Signer 为 nil 时 URL 原样返回；Notifier 为 nil 时回写不广播。
type genExternal struct {
	Storage   generation.AssetStorage
	Assets    generation.AssetRecorder
	Reclaimer generation.ObjectReclaimer
	Signer    genMediaSigner
	Notifier  genCanvasNotifier
}

type genWiring struct {
	Gen       *generation.Service
	CanvasGen *canvasgen.Service
	Routes    apphttp.GenerationRoutes
	Repersist *apphttp.GenerationRepersistHandler
}

// wireGeneration 构造生成链路的全部对象并注册 canvas_node sink。不启动后台循环：
// 调用方在 HTTP 就绪前调用 w.Gen.StartBackground(ctx)。
func wireGeneration(conn *sql.DB, dataDir string, ext genExternal) (*genWiring, error) {
	settingsRepo := settings.NewSQLiteRepository(conn)
	modelsRepo := models.NewSQLiteRepository(conn)
	projectsRepo := projects.NewSQLiteRepository(conn)

	gen := generation.NewService(
		generation.NewSQLiteRepository(conn),
		modelsRepo,
		settingsRepo,
		settingsUpstreamKey{settings: settingsRepo},
		noopGenBilling{svc: billing.NewNoopService()},
		upstream.NewClient(upstream.ClientOptions{Timeout: genUpstreamTimeout}),
		ext.Storage,
		ext.Assets,
		sqliteUserStorage{db: conn},
		ext.Reclaimer,
		conn,
	)
	gen.SetLeaseConfig(genLeaseHeartbeatSec, genLeaseStaleSec)
	tmp := filepath.Join(dataDir, "tmp", "gen")
	if err := generation.PrepareTempDir(tmp, generation.TempDirStaleAfter); err != nil {
		return nil, fmt.Errorf("gen temp dir: %w", err)
	}
	gen.SetTempDir(tmp)
	gen.SetDownloadLimits(genMaxImageOutputBytes, genMaxMediaOutputBytes)
	gen.SetDownloadConcurrency(genDownloadConcurrency)

	cg := canvasgen.NewService(gen, projectsRepo)
	generation.RegisterSink("canvas_node", cg.OnGenerationResult)
	cg.SetSettingsGetter(settingsRepo.Get)
	gen.SetPromptPresetResolver(promptPresetResolver(settingsRepo.Get))
	if ext.Notifier != nil {
		cg.SetChangeNotifier(ext.Notifier)
	}
	if ext.Signer != nil {
		cg.SetMediaSigner(ext.Signer)
	}
	cg.SetSnapshotSearcher(projectsRepo)

	routes := apphttp.GenerationRoutes{
		CanvasGen:  apphttp.NewCanvasGenHandler(cg, ext.Signer),
		Generation: apphttp.NewGenerationHandler(gen, ext.Signer),
		Models:     apphttp.NewRuntimeModelHandler(modelsRepo),
	}
	if ext.Assets != nil {
		routes.Apply = apphttp.NewGenerationApplyHandler(gen, ext.Assets, ext.Signer, conn)
	}
	return &genWiring{
		Gen:       gen,
		CanvasGen: cg,
		Routes:    routes,
		Repersist: apphttp.NewGenerationRepersistHandler(gen, cg, ext.Signer),
	}, nil
}

// promptPresetResolver：多角度/打光保留 key 先分流，
// 其余查绘图预设表；每次现读 settings，后台改正文后排队中的任务执行时自动生效。
func promptPresetResolver(get func(string) (string, error)) func(string, map[string]string) (string, bool) {
	return func(key string, params map[string]string) (string, bool) {
		if key == canvasgen.MultiAnglePresetKey {
			maCfg, err := canvasgen.LoadMultiAngleConfig(get)
			if err != nil {
				return "", false
			}
			if text := maCfg.ComposeMultiAngleText(params); text != "" {
				return text, true
			}
			return "", false
		}
		if key == canvasgen.LightControlPresetKey {
			lcCfg, err := canvasgen.LoadLightControlConfig(get)
			if err != nil {
				return "", false
			}
			if text := lcCfg.ComposeLightControlText(params); text != "" {
				return text, true
			}
			return "", false
		}
		cfg, err := canvasgen.LoadGenImagePresetsConfig(get)
		if err != nil {
			return "", false
		}
		p, ok := cfg.Preset(key)
		if !ok {
			return "", false
		}
		return p.ComposePresetText(params), true
	}
}

// settingsUpstreamKey：桌面端没有「用户独立 key」，全局 Key = settings.upstream_api_key。
// 只作为 models.ResolveUpstream 的回落值（generation / canvasscript 经 GlobalWithKey 接入）。
type settingsUpstreamKey struct {
	settings interface{ Get(string) (string, error) }
}

func (k settingsUpstreamKey) Resolve(int64) (string, error) {
	return k.settings.Get(models.SettingUpstreamAPIKey)
}

// sqliteUserStorage 实现 generation.UserStorage：直接读写 users 表（迁移 0001 已建，本地用户配额 1 PiB）。
type sqliteUserStorage struct{ db *sql.DB }

func (u sqliteUserStorage) AddStorage(id, delta int64) error {
	_, err := u.db.Exec(`UPDATE users SET storage_used_bytes = storage_used_bytes + ? WHERE id = ?`, delta, id)
	return err
}

func (u sqliteUserStorage) StorageUsage(id int64) (int64, int64, error) {
	var used, quota int64
	err := u.db.QueryRow(`SELECT storage_used_bytes, storage_quota_bytes FROM users WHERE id = ?`, id).Scan(&used, &quota)
	return used, quota, err
}

func (u sqliteUserStorage) AddStorageTx(tx *sql.Tx, id, delta int64) error {
	_, err := tx.Exec(`UPDATE users SET storage_used_bytes = storage_used_bytes + ? WHERE id = ?`, delta, id)
	return err
}

// noopGenBilling 把 billing.NoopService 适配成 generation.Billing。
type noopGenBilling struct{ svc *billing.NoopService }

func (b noopGenBilling) Freeze(ctx context.Context, u, t, a int64, n string) (generation.BillingResult, error) {
	_, err := b.svc.Freeze(ctx, u, t, a, n)
	return generation.BillingResult{}, err
}

func (b noopGenBilling) Settle(ctx context.Context, u, t, f, a int64, n string) (generation.BillingResult, error) {
	_, err := b.svc.Settle(ctx, u, t, f, a, n)
	return generation.BillingResult{}, err
}

func (b noopGenBilling) Refund(ctx context.Context, u, t, f int64, n string) (generation.BillingResult, error) {
	_, err := b.svc.Refund(ctx, u, t, f, n)
	return generation.BillingResult{}, err
}

func (b noopGenBilling) FreezeTx(ctx context.Context, tx *sql.Tx, u, t, a int64, n string) (generation.BillingResult, error) {
	_, err := b.svc.FreezeTx(ctx, tx, u, t, a, n)
	return generation.BillingResult{}, err
}

func (b noopGenBilling) SettleTx(ctx context.Context, tx *sql.Tx, u, t, f, a int64, n string) (generation.BillingResult, error) {
	_, err := b.svc.SettleTx(ctx, tx, u, t, f, a, n)
	return generation.BillingResult{}, err
}

func (b noopGenBilling) RefundTx(ctx context.Context, tx *sql.Tx, u, t, f int64, n string) (generation.BillingResult, error) {
	_, err := b.svc.RefundTx(ctx, tx, u, t, f, n)
	return generation.BillingResult{}, err
}
