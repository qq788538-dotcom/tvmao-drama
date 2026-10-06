package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"

	"github.com/james/huabu_studio/apps/api/internal/canvasscript"
	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/settings"
	"github.com/james/huabu_studio/apps/api/internal/skillbundle"
	"github.com/james/huabu_studio/apps/api/internal/skillruntime"
)

// skillMaxIterations 与参考仓 DRAMA_AGENT_MAX_ITERATIONS 默认值一致（重流程 skill 正常就近 20 轮）。
const skillMaxIterations = 40

// wireCanvasScript 装配脚本节点（P6）：释放内置 Skill、找内置 Python、启动回收与 worker。
// 找不到 Python 不阻止启动，只打印警告；任务执行时以“内置 Python 运行时缺失”失败。
func wireCanvasScript(ctx context.Context, conn *sql.DB, dataDir, pythonHome string) (*apphttp.CanvasScriptHandler, error) {
	runtimeDir := filepath.Join(dataDir, "runtime")
	skillsDir, err := skillbundle.Extract(filepath.Join(runtimeDir, "skills"))
	if err != nil {
		return nil, err
	}
	catalog, err := skillruntime.NewCatalog(skillsDir)
	if err != nil {
		return nil, fmt.Errorf("加载内置 Skill: %w", err)
	}
	workspace, err := skillruntime.NewRunWorkspace(filepath.Join(runtimeDir, "skill-runs"))
	if err != nil {
		return nil, fmt.Errorf("创建 Skill Run 工作区: %w", err)
	}
	go skillruntime.NewWorkspaceCleaner(workspace.Root()).Run(ctx)

	python, err := skillruntime.LocatePython(pythonHome)
	if err != nil {
		log.Printf("警告: %v；脚本节点任务将失败", err)
	} else {
		log.Printf("内置 Python: %s", python)
	}
	shellFactory := skillruntime.RestrictedShellFactory(skillruntime.RestrictedShellConfig{Python: python})
	rt := skillruntime.NewRuntime(catalog, workspace, nil, skillMaxIterations, shellFactory)

	settingsRepo := settings.NewSQLiteRepository(conn)
	svc := canvasscript.NewService(conn, settingsRepo, models.NewSQLiteRepository(conn), settingsUpstreamKey{settings: settingsRepo}, rt)
	if err := svc.RecoverOnStartup(ctx); err != nil {
		return nil, fmt.Errorf("回收上次未完成的脚本任务: %w", err)
	}
	go svc.RunWorker(ctx)
	return apphttp.NewCanvasScriptHandler(svc), nil
}
