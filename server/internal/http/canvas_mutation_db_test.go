package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/canvasowner"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/projects"
)

type canvasEnv struct {
	conn      *sql.DB
	handler   *CanvasMutationHandler
	projectID int64
}

func newCanvasEnv(t *testing.T) *canvasEnv {
	t.Helper()
	conn := dbtest.Open(t)
	storage, err := assets.NewLocalStorage(assets.LocalConfig{
		Root: filepath.Join(t.TempDir(), "media"), BaseURL: "http://127.0.0.1:5555", Token: "mt",
	})
	if err != nil {
		t.Fatal(err)
	}
	pr := projects.NewSQLiteRepository(conn)
	pid, err := pr.Create(LocalUserID, "画布", "")
	if err != nil {
		t.Fatal(err)
	}
	return &canvasEnv{conn: conn, handler: NewCanvasMutationHandler(pr, canvasowner.NewLocal(), NewMediaSigner(storage)), projectID: pid}
}

func (e *canvasEnv) req(method, body string, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", strconv.FormatInt(e.projectID, 10))
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = withUserID(ctx, LocalUserID)
	return req.WithContext(withProjectID(ctx, e.projectID))
}

func (e *canvasEnv) storedPayload(t *testing.T) string {
	t.Helper()
	var p string
	if err := e.conn.QueryRow(`SELECT payload FROM workflow_snapshots WHERE project_id = ?`, e.projectID).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

// Review Focus 2：前端带着上一次启动的 URL（端口、token 都已变）提交，落库必须是 media://。
func TestCanvasDBMutationPersistsCanonicalMediaFromAnyPort(t *testing.T) {
	e := newCanvasEnv(t)
	rec := httptest.NewRecorder()
	e.handler.ClaimOwner(rec, e.req(http.MethodPost, "", nil))
	var claim struct {
		OwnerToken string `json:"ownerToken"`
	}
	json.Unmarshal(rec.Body.Bytes(), &claim)
	if rec.Code != http.StatusOK || claim.OwnerToken == "" {
		t.Fatalf("claim code=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	e.handler.UpsertNode(rec, e.req(http.MethodPost,
		`{"ownerToken":"`+claim.OwnerToken+`","node":{"id":"n1","type":"image","content":"http://127.0.0.1:61000/media/u1/2026/10/a.png?token=stale"}}`, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert code=%d body=%s", rec.Code, rec.Body.String())
	}
	p := e.storedPayload(t)
	if !strings.Contains(p, `"media://u1/2026/10/a.png"`) || strings.Contains(p, "61000") || strings.Contains(p, "token=") {
		t.Fatalf("落库 payload=%s", p)
	}

	rec = httptest.NewRecorder()
	e.handler.PatchNode(rec, e.req(http.MethodPatch,
		`{"ownerToken":"x","patch":{"content":"http://127.0.0.1:5555/media/u1/b.png?token=mt"}}`, map[string]string{"nodeID": "n1"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch code=%d body=%s", rec.Code, rec.Body.String())
	}
	if p := e.storedPayload(t); !strings.Contains(p, `"media://u1/b.png"`) || strings.Contains(p, "5555") {
		t.Fatalf("落库 payload=%s", p)
	}
}

func TestCanvasDBLocalOwnerNeverBlocksWrites(t *testing.T) {
	e := newCanvasEnv(t)
	rec := httptest.NewRecorder()
	e.handler.UpsertNode(rec, e.req(http.MethodPost, `{"node":{"id":"n1","type":"text"}}`, nil)) // 没有 ownerToken
	if rec.Code != http.StatusOK {
		t.Fatalf("无 token 写入 code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	e.handler.ProbeOwner(rec, e.req(http.MethodGet, "", nil))
	var probe map[string]any
	json.Unmarshal(rec.Body.Bytes(), &probe)
	if rec.Code != http.StatusOK || probe["held"] != false {
		t.Fatalf("probe code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	e.handler.HeartbeatOwner(rec, e.req(http.MethodPost, `{"ownerToken":""}`, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat code=%d", rec.Code)
	}
}

func TestCanvasDBAssetRefStoreAndImport(t *testing.T) {
	conn := dbtest.Open(t)
	pr := projects.NewSQLiteRepository(conn)
	pa, _ := pr.Create(LocalUserID, "A", "")
	pb, _ := pr.Create(LocalUserID, "B", "")
	ar := assets.NewSQLiteRepository(conn)
	ar.Insert(assets.AssetRecord{UserID: LocalUserID, ProjectID: pa, ObjectKey: "u1/a.png", SourceKind: "upload",
		URL: "media://u1/a.png", SizeBytes: 9, MimeType: "image/png"})

	store := NewSQLCanvasAssetRefStore(conn)
	if owner, err := store.ProjectOwner(pa); err != nil || owner != LocalUserID {
		t.Fatalf("owner=%d err=%v", owner, err)
	}
	if _, err := store.ProjectOwner(99999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err=%v want ErrNoRows", err)
	}
	if url, mime, found, err := store.CanonicalByKey(LocalUserID, "u1/a.png"); err != nil || !found || url != "media://u1/a.png" || mime != "image/png" {
		t.Fatalf("url=%q mime=%q found=%v err=%v", url, mime, found, err)
	}
	if _, _, found, _ := store.CanonicalByKey(LocalUserID, "u1/none.png"); found {
		t.Fatal("不存在的 key 不应 found")
	}
	if has, err := store.ProjectHasKey(pa, "u1/a.png"); err != nil || !has {
		t.Fatalf("has=%v err=%v", has, err)
	}
	if has, _ := store.ProjectHasKey(pb, "u1/a.png"); has {
		t.Fatal("B 还没有这个 key")
	}

	h := NewCanvasAssetImportHandler(store, ar)
	for i := 0; i < 2; i++ { // 重复粘贴必须幂等
		rec := doImportRefs(t, h, LocalUserID, pb, map[string]any{"objectKeys": []string{"u1/a.png", "u1/none.png"}})
		var res importAssetRefsResult
		json.Unmarshal(rec.Body.Bytes(), &res)
		if rec.Code != http.StatusOK || res.Imported != 1 || !reflect.DeepEqual(res.Missing, []string{"u1/none.png"}) {
			t.Fatalf("第 %d 次 code=%d res=%+v", i+1, rec.Code, res)
		}
	}
	rows, _ := ar.ListForProject(pb)
	if len(rows) != 1 || rows[0].SourceKind != "canvas_ref" || rows[0].SizeBytes != 0 {
		t.Fatalf("B 的引用行=%+v", rows)
	}
}
