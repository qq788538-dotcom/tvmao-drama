package http

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/projects"
	"github.com/james/huabu_studio/apps/api/internal/users"
	"github.com/james/huabu_studio/apps/api/internal/workflows"
)

type projectEnv struct {
	conn     *sql.DB
	storage  *assets.LocalStorage
	projects *projects.SQLiteRepository
	assets   *assets.SQLiteRepository
	users    *users.SQLiteRepository
	handler  *ProjectHandler
	cleaner  *ProjectCleaner
}

func newProjectEnv(t *testing.T) *projectEnv {
	t.Helper()
	conn := dbtest.Open(t)
	storage, err := assets.NewLocalStorage(assets.LocalConfig{
		Root: filepath.Join(t.TempDir(), "media"), BaseURL: "http://127.0.0.1:5555", Token: "mt",
	})
	if err != nil {
		t.Fatal(err)
	}
	pr := projects.NewSQLiteRepository(conn)
	ar := assets.NewSQLiteRepository(conn)
	ur := users.NewSQLiteRepository(conn)
	orphans := library.NewSQLiteOrphansRepository(conn)
	dup := workflows.NewAssetDuplicator(storage, ar, ur, workflows.NewDeleteNowQueue(storage))
	return &projectEnv{
		conn: conn, storage: storage, projects: pr, assets: ar, users: ur,
		handler: NewProjectHandler(pr, NewMediaSigner(storage), storage, ar, ur, nil, orphans, conn, dup),
		cleaner: NewProjectCleaner(pr, ar, ur, nil, orphans, conn),
	}
}

// putAsset 写文件 + 插 asset 行 + 记账，模拟一次真实上传。
func (e *projectEnv) putAsset(t *testing.T, projectID int64, key, mime string, body []byte) int64 {
	t.Helper()
	if _, err := e.storage.PutObject(context.Background(), assets.PutObjectInput{Key: key, ContentType: mime, Body: body}); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	id, err := e.assets.Insert(assets.AssetRecord{UserID: LocalUserID, ProjectID: projectID, ObjectKey: key, SourceKind: "upload",
		URL: "media://" + key, SizeBytes: int64(len(body)), MimeType: mime, ContentHash: hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.users.AddStorage(LocalUserID, int64(len(body))); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *projectEnv) used(t *testing.T) int64 {
	t.Helper()
	used, _, err := e.users.StorageUsage(LocalUserID)
	if err != nil {
		t.Fatal(err)
	}
	return used
}

func projectReq(method string, projectID int64, body string) *http.Request {
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	ctx := withUserID(req.Context(), LocalUserID)
	return req.WithContext(withProjectID(ctx, projectID))
}

func TestProjectCRUDHandlers(t *testing.T) {
	e := newProjectEnv(t)
	rec := httptest.NewRecorder()
	e.handler.Create(rec, projectReq(http.MethodPost, 0, `{"name":""}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空名字 code=%d want 400", rec.Code)
	}
	rec = httptest.NewRecorder()
	e.handler.Create(rec, projectReq(http.MethodPost, 0, `{"name":"未命名项目"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create code=%d body=%s", rec.Code, rec.Body.String())
	}
	var p projects.Project
	json.Unmarshal(rec.Body.Bytes(), &p)
	rec = httptest.NewRecorder()
	e.handler.Update(rec, projectReq(http.MethodPut, p.ID, `{"name":"改名","description":"d"}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "改名") {
		t.Fatalf("update code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	e.handler.Delete(rec, projectReq(http.MethodDelete, p.ID, ""))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete code=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	e.handler.Get(rec, projectReq(http.MethodGet, p.ID, ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("软删后 get code=%d want 404", rec.Code)
	}
}

func TestProjectGetSnapshotSignsMedia(t *testing.T) {
	e := newProjectEnv(t)
	pid, _ := e.projects.Create(LocalUserID, "p", "")
	e.projects.SaveAuthoritative(pid, json.RawMessage(`{"nodes":[{"id":"n1","content":"media://u1/a.png"}]}`))
	rec := httptest.NewRecorder()
	e.handler.GetSnapshot(rec, projectReq(http.MethodGet, pid, ""))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "http://127.0.0.1:5555/media/u1/a.png?token=mt") || strings.Contains(body, "media://") {
		t.Fatalf("快照出口必须换成本地 URL：code=%d body=%s", rec.Code, body)
	}
}

func TestProjectDuplicateCopiesMediaAndRewritesSnapshot(t *testing.T) {
	e := newProjectEnv(t)
	pid, _ := e.projects.Create(LocalUserID, "原项目", "说明")
	e.putAsset(t, pid, "u1/2026/10/a.png", "image/png", []byte("png-a"))
	if _, err := e.projects.SaveAuthoritative(pid, json.RawMessage(
		`{"nodes":[{"id":"n1","content":"media://u1/2026/10/a.png"},{"id":"n2","content":"https://cdn.example/x.png"}],"connections":[]}`)); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	e.handler.Duplicate(rec, projectReq(http.MethodPost, pid, ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		ProjectID int64 `json:"projectId"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	p, err := e.projects.GetForUser(LocalUserID, out.ProjectID)
	if err != nil || p.Name != "原项目 - 副本" || p.Description != "说明" {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	snap, err := e.projects.GetLatest(out.ProjectID)
	if err != nil || snap == nil {
		t.Fatalf("副本没有快照: %v", err)
	}
	var doc struct {
		Nodes []struct{ ID, Content string }
	}
	json.Unmarshal(snap.Payload, &doc)
	newRef := doc.Nodes[0].Content
	if !strings.HasPrefix(newRef, "media://u1/") || newRef == "media://u1/2026/10/a.png" {
		t.Fatalf("副本应引用新 key，got %q", newRef)
	}
	if doc.Nodes[1].Content != "https://cdn.example/x.png" {
		t.Fatalf("外部 URL 应原样保留，got %q", doc.Nodes[1].Content)
	}
	rc, err := e.storage.GetObject(context.Background(), strings.TrimPrefix(newRef, "media://"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "png-a" {
		t.Fatalf("副本内容=%q", b)
	}
	if got := e.used(t); got != 10 {
		t.Fatalf("副本应真实占用配额：used=%d want 10", got)
	}
	if rows, _ := e.assets.ListForProject(out.ProjectID); len(rows) != 1 || rows[0].SizeBytes != 5 {
		t.Fatalf("副本 asset 行=%+v", rows)
	}
}

func TestProjectDuplicateEmptyProject(t *testing.T) {
	e := newProjectEnv(t)
	pid, _ := e.projects.Create(LocalUserID, "空", "")
	rec := httptest.NewRecorder()
	e.handler.Duplicate(rec, projectReq(http.MethodPost, pid, ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		ProjectID int64 `json:"projectId"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if snap, _ := e.projects.GetLatest(out.ProjectID); snap != nil {
		t.Fatal("空项目的副本不应有快照")
	}
}

// 删项目只回收不再被任何地方引用的文件：被别的项目粘贴引用的必须留下（否则那边的节点变死链）。
func TestProjectDeletedIsPurgedAndSharedObjectsSurvive(t *testing.T) {
	e := newProjectEnv(t)
	pa, _ := e.projects.Create(LocalUserID, "A", "")
	pb, _ := e.projects.Create(LocalUserID, "B", "")
	e.putAsset(t, pa, "u1/shared.png", "image/png", []byte("12345"))
	e.putAsset(t, pa, "u1/only-a.png", "image/png", []byte("1234567"))
	e.assets.Insert(assets.AssetRecord{UserID: LocalUserID, ProjectID: pb, ObjectKey: "u1/shared.png", SourceKind: "canvas_ref",
		URL: "media://u1/shared.png", SizeBytes: 0, MimeType: "image/png"})
	e.projects.SaveAuthoritative(pa, json.RawMessage(`{"nodes":[]}`))

	rec := httptest.NewRecorder()
	e.handler.Delete(rec, projectReq(http.MethodDelete, pa, ""))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d", rec.Code)
	}
	if _, err := e.projects.GetForUser(LocalUserID, pa); !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("软删后应立即读不到: %v", err)
	}
	if err := e.cleaner.purgePending(10); err != nil {
		t.Fatal(err)
	}
	if rows, _ := e.assets.ListForProject(pa); len(rows) != 0 {
		t.Fatalf("A 的 asset 行没删干净: %+v", rows)
	}
	var orphanKeys []string
	rows, _ := e.conn.Query(`SELECT object_key FROM orphan_objects ORDER BY object_key`)
	for rows.Next() {
		var k string
		rows.Scan(&k)
		orphanKeys = append(orphanKeys, k)
	}
	rows.Close()
	if !reflect.DeepEqual(orphanKeys, []string{"u1/only-a.png"}) {
		t.Fatalf("孤儿=%v，want 只有 only-a（shared 仍被 B 引用）", orphanKeys)
	}
	if got := e.used(t); got != 5 {
		t.Fatalf("只应回收 only-a 的 7 字节：used=%d want 5", got)
	}
	var n int
	e.conn.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = ?`, pa).Scan(&n)
	if n != 0 {
		t.Fatal("项目行没有硬删")
	}
	e.conn.QueryRow(`SELECT COUNT(*) FROM workflow_snapshots WHERE project_id = ?`, pa).Scan(&n)
	if n != 0 {
		t.Fatal("快照没有随项目级联删除")
	}
	// 文件本身留给 7 天后的孤儿清扫（支持撤销），此刻都还在。
	for _, k := range []string{"u1/shared.png", "u1/only-a.png"} {
		p, _ := e.storage.Path(k)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s 不应被立即删除: %v", k, err)
		}
	}
	if rows, _ := e.assets.ListForProject(pb); len(rows) != 1 {
		t.Fatalf("B 的引用行被误删: %+v", rows)
	}
}

func TestProjectListImagesAndSetCover(t *testing.T) {
	e := newProjectEnv(t)
	pid, _ := e.projects.Create(LocalUserID, "p", "")
	img := e.putAsset(t, pid, "u1/cover.png", "image/png", []byte("img"))
	vid := e.putAsset(t, pid, "u1/clip.mp4", "video/mp4", []byte("vid"))
	other, _ := e.projects.Create(LocalUserID, "other", "")
	foreign := e.putAsset(t, other, "u1/foreign.png", "image/png", []byte("f"))

	rec := httptest.NewRecorder()
	e.handler.ListImages(rec, projectReq(http.MethodGet, pid, ""))
	var list struct {
		Data []projectImage `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Data) != 1 || list.Data[0].ID != img || !strings.HasPrefix(list.Data[0].URL, "http://127.0.0.1:5555/media/u1/cover.png") {
		t.Fatalf("images=%+v", list.Data)
	}

	rec = httptest.NewRecorder()
	e.handler.SetCover(rec, projectReq(http.MethodPut, pid, fmt.Sprintf(`{"assetId":%d}`, img)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "http://127.0.0.1:5555/media/u1/cover.png") {
		t.Fatalf("setCover code=%d body=%s", rec.Code, rec.Body.String())
	}
	var stored string
	e.conn.QueryRow(`SELECT cover_url FROM projects WHERE id = ?`, pid).Scan(&stored)
	if stored != "media://u1/cover.png" {
		t.Fatalf("库里必须存 canonical，got %q", stored)
	}
	rec = httptest.NewRecorder()
	e.handler.List(rec, projectReq(http.MethodGet, 0, ""))
	var page struct {
		Data []projects.Project `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &page)
	found := false
	for _, p := range page.Data {
		if p.ID == pid {
			found = strings.HasPrefix(p.CoverURL, "http://127.0.0.1:5555/media/u1/cover.png")
		}
	}
	if !found {
		t.Fatalf("列表封面没有签名: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	e.handler.SetCover(rec, projectReq(http.MethodPut, pid, fmt.Sprintf(`{"assetId":%d}`, vid)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("视频做封面 code=%d want 400", rec.Code)
	}
	rec = httptest.NewRecorder()
	e.handler.SetCover(rec, projectReq(http.MethodPut, pid, fmt.Sprintf(`{"assetId":%d}`, foreign)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("别的项目的图 code=%d want 404", rec.Code)
	}
}

// 关闭时 main 要等清理 worker 真正退出再关库：Start 返回的通道在 ctx 取消后关闭。
func TestProjectCleanerStartDoneClosesAfterCancel(t *testing.T) {
	e := newProjectEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := e.cleaner.Start(ctx, 0)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消 3 秒后清理 worker 仍未退出")
	}
}
