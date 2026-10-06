package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/db"
)

func TestWireCanvasScriptExtractsSkillsAndRecoversRunningTasks(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "Application Support", "com.miaoju.desktop")
	conn, err := db.Open(filepath.Join(dataDir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	res, err := conn.Exec(`INSERT INTO canvas_script_tasks (user_id, project_id, node_id, skill_name, duration_sec, script, model, status, attempts, owner_instance)
		VALUES (1, 7, 'n', 'real-person-short-drama-v3', 10, '剧本', 'canvas-gpt-5.5', 'running', 1, 'old')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, _, err := wireCanvasScript(ctx, conn, dataDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if h == nil {
		t.Fatal("handler 不应为 nil")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "runtime", "skills", "real-person-short-drama-v3", "scripts", "validate_storyboard.py")); err != nil {
		t.Fatalf("内置 Skill 应释放到数据目录: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "runtime", "skill-runs")); err != nil {
		t.Fatalf("Run 工作区根目录应已创建: %v", err)
	}
	var status string
	if err := conn.QueryRow(`SELECT status FROM canvas_script_tasks WHERE id=?`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("上个进程遗留的 running 应回到 pending，got %s", status)
	}
}

// 关闭 ctx 后，脚本节点 worker / 清理器 / 在跑任务必须全部退出（关库前 waitDone 可等到），
// 之后关库不会再有 goroutine 写库。
func TestWireCanvasScriptWorkersExitOnCancel(t *testing.T) {
	dataDir := t.TempDir()
	conn, err := db.Open(filepath.Join(dataDir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, workers, err := wireCanvasScript(ctx, conn, dataDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) == 0 {
		t.Fatal("应返回后台 worker 的退出信号")
	}
	cancel()
	if !waitDone(2*time.Second, workers...) {
		t.Fatal("cancel 后 worker 未在期限内退出")
	}
	conn.Close()
	time.Sleep(50 * time.Millisecond) // 若仍有 goroutine 写库，此处会在日志里出现 closed
}
