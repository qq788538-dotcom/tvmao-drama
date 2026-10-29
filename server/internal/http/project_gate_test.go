package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasgen"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/cliapi"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/projects"
)

// fakeProjectGate 记录被问到的项目 id；active 里的算有效，err 非 nil 模拟查库失败。
type fakeProjectGate struct {
	active map[int64]bool
	err    error
	asked  []int64
}

func (g *fakeProjectGate) ActiveExists(_ context.Context, id int64) (bool, error) {
	g.asked = append(g.asked, id)
	return g.active[id], g.err
}

func TestCheckProjectActive(t *testing.T) {
	ctx := context.Background()
	g := &fakeProjectGate{active: map[int64]bool{1: true}}
	if err := checkProjectActive(ctx, g, 0); err != nil || len(g.asked) != 0 {
		t.Fatalf("projectID=0 应跳过且不查库: err=%v asked=%v", err, g.asked)
	}
	if err := checkProjectActive(ctx, nil, 5); err != nil {
		t.Fatalf("未接线应跳过: %v", err)
	}
	if err := checkProjectActive(ctx, g, 1); err != nil {
		t.Fatalf("有效项目应放行: %v", err)
	}
	if err := checkProjectActive(ctx, g, 2); !errors.Is(err, errProjectGone) {
		t.Fatalf("无效项目应返回 errProjectGone: %v", err)
	}
	boom := errors.New("db down")
	if err := checkProjectActive(ctx, &fakeProjectGate{err: boom}, 1); !errors.Is(err, boom) {
		t.Fatalf("查库失败应原样返回: %v", err)
	}
}

// 真库：软删后的项目拒绝，与 projects.ActiveExists 的口径一致。
func TestCheckProjectActiveWithSQLiteRepo(t *testing.T) {
	conn := dbtest.Open(t)
	repo := projects.NewSQLiteRepository(conn)
	id, err := repo.Create(LocalUserID, "p", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkProjectActive(context.Background(), repo, id); err != nil {
		t.Fatalf("新建项目应放行: %v", err)
	}
	if err := repo.Delete(LocalUserID, id); err != nil {
		t.Fatal(err)
	}
	if err := checkProjectActive(context.Background(), repo, id); !errors.Is(err, errProjectGone) {
		t.Fatalf("软删后应拒绝: %v", err)
	}
}

func canvasGenSubmitReq(projectID string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/api/v1/canvas-tasks",
		strings.NewReader(`{"projectId":`+projectID+`,"nodeId":"n1","modelId":"m","inputs":{}}`))
}

func TestCanvasGenSubmitRejectsGoneProject(t *testing.T) {
	h := NewCanvasGenHandler(canvasgen.NewService(fakeGenClient{err: errors.New("不应走到入队")}, nil), nil).
		WithProjectGate(&fakeProjectGate{active: map[int64]bool{}})
	rec := httptest.NewRecorder()
	h.Submit(rec, canvasGenSubmitReq("3"))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "已被删除") {
		t.Fatalf("已删项目应 404 + 可读文案, got %d: %s", rec.Code, rec.Body.String())
	}

	// 查库失败：500，不透出底层错误。
	h.WithProjectGate(&fakeProjectGate{err: errors.New("sqlite busy")})
	rec = httptest.NewRecorder()
	h.Submit(rec, canvasGenSubmitReq("3"))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "sqlite") {
		t.Fatalf("查库失败应 500 且不透出细节, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCanvasGenSubmitActiveProjectPassesGate(t *testing.T) {
	gate := &fakeProjectGate{active: map[int64]bool{3: true}}
	msg := "该模型最多支持连接 1 个视频，当前连接了 2 个"
	h := NewCanvasGenHandler(canvasgen.NewService(fakeGenClient{err: &generation.InputLimitError{Msg: msg}}, nil), nil).
		WithProjectGate(gate)
	rec := httptest.NewRecorder()
	h.Submit(rec, canvasGenSubmitReq("3"))
	// 过了校验才会走到入队、拿到 InputLimitError 的 400。
	if rec.Code != http.StatusBadRequest || len(gate.asked) != 1 || gate.asked[0] != 3 {
		t.Fatalf("有效项目应放行到入队: code=%d asked=%v body=%s", rec.Code, gate.asked, rec.Body.String())
	}
}

// 上传（网页与 CLI 共用 storeUpload）：已删项目 404 且不落库；projectID=0 不挂项目，照常上传。
func TestStoreUploadRejectsGoneProject(t *testing.T) {
	gate := &fakeProjectGate{active: map[int64]bool{5: true}}
	h := newTestAssetHandler(t).WithProjectGate(gate)
	inserted := 0
	h.repo.(*fakeAssetRepo).onInsert = func(assets.AssetRecord) { inserted++ }
	ctx := context.Background()

	_, err := h.storeUpload(ctx, 7, 6, "a.png", "image/png", []byte("png"))
	var ue *uploadError
	if !errors.As(err, &ue) || ue.Status != http.StatusNotFound || !strings.Contains(ue.Message, "已被删除") || inserted != 0 {
		t.Fatalf("已删项目应 404 且不落库: err=%v inserted=%d", err, inserted)
	}
	// CLI 侧映射成 PROJECT_NOT_FOUND，与 RunNode 一致。
	var ce *cliapi.Error
	if !errors.As(mapUploadError(err), &ce) || ce.Code != cliapi.CodeProjectNotFound {
		t.Fatalf("CLI 应映射为 PROJECT_NOT_FOUND: %v", mapUploadError(err))
	}

	if _, err := h.storeUpload(ctx, 7, 5, "a.png", "image/png", []byte("png")); err != nil || inserted != 1 {
		t.Fatalf("有效项目应上传成功: err=%v inserted=%d", err, inserted)
	}
	if _, err := h.storeUpload(ctx, 7, 0, "b.png", "image/png", []byte("png2")); err != nil || inserted != 2 {
		t.Fatalf("projectID=0 应跳过校验: err=%v inserted=%d", err, inserted)
	}
}

// 删项目时一并删掉该项目的脚本节点任务，别的项目的不动。
func TestProjectCleanerDeletesCanvasScriptTasks(t *testing.T) {
	e := newProjectEnv(t)
	pa, _ := e.projects.Create(LocalUserID, "A", "")
	pb, _ := e.projects.Create(LocalUserID, "B", "")
	for _, pid := range []int64{pa, pa, pb} {
		if _, err := e.conn.Exec(`INSERT INTO canvas_script_tasks (user_id, project_id, node_id, skill_name, duration_sec, script, model)
			VALUES (?, ?, 'n', 's', 10, '剧本', 'm')`, LocalUserID, pid); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.projects.Delete(LocalUserID, pa); err != nil {
		t.Fatal(err)
	}
	if err := e.cleaner.purgePending(10); err != nil {
		t.Fatal(err)
	}
	var na, nb int
	e.conn.QueryRow(`SELECT COUNT(*) FROM canvas_script_tasks WHERE project_id=?`, pa).Scan(&na)
	e.conn.QueryRow(`SELECT COUNT(*) FROM canvas_script_tasks WHERE project_id=?`, pb).Scan(&nb)
	if na != 0 || nb != 1 {
		t.Fatalf("A 的脚本任务应删光、B 的保留: A=%d B=%d", na, nb)
	}
}
