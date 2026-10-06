package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/projects"
	"github.com/james/huabu_studio/apps/api/internal/users"
)

var libraryPNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 16)...)

type libraryEnv struct {
	conn     *sql.DB
	storage  *assets.LocalStorage
	signer   *MediaSigner
	folders  *library.SQLiteFoldersRepository
	assets   *assets.SQLiteRepository
	users    *users.SQLiteRepository
	projects *projects.SQLiteRepository
	router   http.Handler
}

func newLibraryEnv(t *testing.T) *libraryEnv {
	t.Helper()
	conn := dbtest.Open(t)
	storage, err := assets.NewLocalStorage(assets.LocalConfig{
		Root: filepath.Join(t.TempDir(), "media"), BaseURL: "http://127.0.0.1:5555", Token: "mt",
	})
	if err != nil {
		t.Fatal(err)
	}
	folders := library.NewSQLiteFoldersRepository(conn)
	items := library.NewSQLiteItemsRepository(conn)
	orphans := library.NewSQLiteOrphansRepository(conn)
	ar := assets.NewSQLiteRepository(conn)
	ur := users.NewSQLiteRepository(conn)
	signer := NewMediaSigner(storage)
	folderH := NewLibraryFolderHandler(folders, items, orphans, ur, conn)
	itemH := NewLibraryItemHandler(items, folders, orphans, storage, ur, signer, conn)
	bridge := NewLibraryBridgeHandler(items, folders, ar, signer, conn)
	gc := NewAdminGCHandler(library.NewOrphanSweeper(orphans, storage, conn))

	// 注册方式与 Task 12 的 router.go 一致（参考仓 router.go 素材库段）。
	r := chi.NewRouter()
	r.Use(LocalIdentity)
	r.Route("/library", func(r chi.Router) {
		r.Get("/folders", folderH.List)
		r.Post("/folders", folderH.Create)
		r.Patch("/folders/{folderID}", folderH.Update)
		r.Delete("/folders/{folderID}", folderH.Delete)
		r.Post("/folders/{folderID}/items/upload", itemH.Upload)
		r.Get("/items", itemH.List)
		r.Patch("/items/{itemID}", itemH.Update)
		r.Delete("/items/{itemID}", itemH.Delete)
		r.Post("/items/{itemID}/apply-to-canvas", bridge.ApplyToCanvas)
	})
	r.Post("/projects/{projectID}/assets/{assetID}/save-to-library", bridge.SaveToLibrary)
	r.Post("/library/save-from-url", bridge.SaveFromURL)
	r.Post("/admin/gc", gc.Run)

	return &libraryEnv{conn: conn, storage: storage, signer: signer, folders: folders, assets: ar, users: ur,
		projects: projects.NewSQLiteRepository(conn), router: r}
}

func (e *libraryEnv) do(method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *libraryEnv) doJSON(method, path, body string) *httptest.ResponseRecorder {
	return e.do(method, path, strings.NewReader(body), "application/json")
}

func (e *libraryEnv) upload(t *testing.T, folderID int64, filename, contentType string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="` + filename + `"`},
		"Content-Type":        {contentType},
	})
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	mw.Close()
	return e.do(http.MethodPost, fmt.Sprintf("/library/folders/%d/items/upload", folderID), &buf, mw.FormDataContentType())
}

type libraryItemJSON struct {
	ID        int64  `json:"id"`
	URL       string `json:"url"`
	SizeBytes int64  `json:"sizeBytes"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	Name      string `json:"name"`
}

func (e *libraryEnv) uploadOK(t *testing.T, folderID int64) libraryItemJSON {
	t.Helper()
	rec := e.upload(t, folderID, "主角.png", "image/png", libraryPNG)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload code=%d body=%s", rec.Code, rec.Body.String())
	}
	var it libraryItemJSON
	json.Unmarshal(rec.Body.Bytes(), &it)
	return it
}

func (e *libraryEnv) mkFolder(t *testing.T, name string, parent *int64) int64 {
	t.Helper()
	id, err := e.folders.Create(LocalUserID, parent, name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *libraryEnv) used(t *testing.T) int64 {
	t.Helper()
	used, _, err := e.users.StorageUsage(LocalUserID)
	if err != nil {
		t.Fatal(err)
	}
	return used
}

func (e *libraryEnv) orphanKeys(t *testing.T) []string {
	t.Helper()
	rows, err := e.conn.Query(`SELECT object_key FROM orphan_objects ORDER BY object_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		rows.Scan(&k)
		out = append(out, k)
	}
	return out
}

func TestLibraryUploadStoresFileAndChargesQuota(t *testing.T) {
	e := newLibraryEnv(t)
	fid := e.mkFolder(t, "角色", nil)
	it := e.uploadOK(t, fid)
	if !strings.HasPrefix(it.URL, "http://127.0.0.1:5555/media/library/1/") || !strings.Contains(it.URL, ".png?token=mt") {
		t.Fatalf("url=%q", it.URL)
	}
	if it.Kind != "image" || it.Name != "主角" || it.SizeBytes != int64(len(libraryPNG)) || it.Source != "upload" {
		t.Fatalf("item=%+v", it)
	}
	rc, err := e.storage.GetObject(context.Background(), e.signer.ObjectKey(it.URL))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(b, libraryPNG) {
		t.Fatal("落盘内容不一致")
	}
	if got := e.used(t); got != int64(len(libraryPNG)) {
		t.Fatalf("used=%d", got)
	}
	rec := e.do(http.MethodGet, fmt.Sprintf("/library/items?folder_id=%d", fid), nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":1`) || !strings.Contains(rec.Body.String(), "http://127.0.0.1:5555/media/library/1/") {
		t.Fatalf("list code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLibraryUploadRejectsUnsupportedMime(t *testing.T) {
	e := newLibraryEnv(t)
	fid := e.mkFolder(t, "f", nil)
	if rec := e.upload(t, fid, "notes.txt", "text/plain", []byte("hi")); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("code=%d want 415", rec.Code)
	}
	if got := e.used(t); got != 0 {
		t.Fatalf("被拒的上传不应记账，used=%d", got)
	}
	if _, err := os.Stat(filepath.Join(e.storage.Root(), "library")); !os.IsNotExist(err) {
		t.Fatal("被拒的上传不应落盘")
	}
}

func TestLibraryItemDeleteMarksOrphanAndFreesQuota(t *testing.T) {
	e := newLibraryEnv(t)
	fid := e.mkFolder(t, "f", nil)
	it := e.uploadOK(t, fid)
	key := e.signer.ObjectKey(it.URL)
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/library/items/%d", it.ID), nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := e.orphanKeys(t); !reflect.DeepEqual(got, []string{key}) {
		t.Fatalf("孤儿=%v want [%s]", got, key)
	}
	if got := e.used(t); got != 0 {
		t.Fatalf("used=%d want 0", got)
	}
	p, _ := e.storage.Path(key)
	if _, err := os.Stat(p); err != nil {
		t.Fatal("文件要等 7 天宽限期后才删（支持撤销）")
	}
}

// 素材应用到画布后再从素材库删掉：画布还在用，文件不能进孤儿队列，配额也不退。
func TestLibraryItemDeleteKeepsObjectUsedOnCanvas(t *testing.T) {
	e := newLibraryEnv(t)
	fid := e.mkFolder(t, "f", nil)
	it := e.uploadOK(t, fid)
	pid, _ := e.projects.Create(LocalUserID, "画布", "")
	rec := e.doJSON(http.MethodPost, fmt.Sprintf("/library/items/%d/apply-to-canvas", it.ID), fmt.Sprintf(`{"projectId":%d}`, pid))
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "http://127.0.0.1:5555/media/library/1/") {
		t.Fatalf("apply code=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, _ := e.assets.ListForProject(pid)
	if len(rows) != 1 || rows[0].SourceKind != "library_ref" || rows[0].SizeBytes != 0 {
		t.Fatalf("引用行=%+v", rows)
	}
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/library/items/%d", it.ID), nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete code=%d", rec.Code)
	}
	if got := e.orphanKeys(t); len(got) != 0 {
		t.Fatalf("画布仍在用，不应标孤儿：%v", got)
	}
	if got := e.used(t); got != int64(len(libraryPNG)) {
		t.Fatalf("配额不应退回，used=%d", got)
	}
	var n int
	e.conn.QueryRow(`SELECT COUNT(*) FROM assets WHERE library_item_id IS NOT NULL`).Scan(&n)
	if n != 0 {
		t.Fatal("删除素材后 assets.library_item_id 应清空")
	}
}

func TestLibraryFolderDeleteNonEmptyAndForce(t *testing.T) {
	e := newLibraryEnv(t)
	parent := e.mkFolder(t, "父", nil)
	child := e.mkFolder(t, "子", &parent)
	it := e.uploadOK(t, child)
	key := e.signer.ObjectKey(it.URL)

	rec := e.do(http.MethodDelete, fmt.Sprintf("/library/folders/%d", parent), nil, "")
	var conflict map[string]any
	json.Unmarshal(rec.Body.Bytes(), &conflict)
	if rec.Code != http.StatusConflict || conflict["childCount"] != float64(1) || conflict["itemCount"] != float64(0) {
		t.Fatalf("非空目录 code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodDelete, fmt.Sprintf("/library/folders/%d?force=1", parent), nil, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("force code=%d body=%s", rec.Code, rec.Body.String())
	}
	if list, _ := e.folders.List(LocalUserID); len(list) != 0 {
		t.Fatalf("目录树没删干净: %+v", list)
	}
	if got := e.orphanKeys(t); !reflect.DeepEqual(got, []string{key}) {
		t.Fatalf("孤儿=%v", got)
	}
	if got := e.used(t); got != 0 {
		t.Fatalf("used=%d", got)
	}
}

// 节点只持有 URL：按 URL 存入素材库必须认得本次和上一次启动发出的本地 URL。
func TestLibrarySaveFromURLAcceptsLocalURLs(t *testing.T) {
	e := newLibraryEnv(t)
	fid := e.mkFolder(t, "f", nil)
	pid, _ := e.projects.Create(LocalUserID, "p", "")
	e.storage.PutObject(context.Background(), assets.PutObjectInput{Key: "u1/a.png", Body: libraryPNG})
	aid, _ := e.assets.Insert(assets.AssetRecord{UserID: LocalUserID, ProjectID: pid, ObjectKey: "u1/a.png", SourceKind: "upload",
		URL: "media://u1/a.png", SizeBytes: int64(len(libraryPNG)), MimeType: "image/png"})

	for _, u := range []string{e.signer.Sign("media://u1/a.png"), "http://127.0.0.1:40000/media/u1/a.png?token=old"} {
		rec := e.doJSON(http.MethodPost, "/library/save-from-url", fmt.Sprintf(`{"url":%q,"folderId":%d}`, u, fid))
		var it libraryItemJSON
		json.Unmarshal(rec.Body.Bytes(), &it)
		if rec.Code != http.StatusCreated || it.SizeBytes != 0 || it.Source != "from_project" {
			t.Fatalf("url=%s code=%d body=%s", u, rec.Code, rec.Body.String())
		}
		var linked int64
		e.conn.QueryRow(`SELECT library_item_id FROM assets WHERE id = ?`, aid).Scan(&linked)
		if linked != it.ID {
			t.Fatalf("asset 应盖上 library_item_id=%d，got %d", it.ID, linked)
		}
	}
	if rec := e.doJSON(http.MethodPost, "/library/save-from-url", fmt.Sprintf(`{"url":"https://cdn.example/x.png","folderId":%d}`, fid)); rec.Code != http.StatusBadRequest {
		t.Fatalf("外部 URL code=%d want 400", rec.Code)
	}
	if rec := e.doJSON(http.MethodPost, "/library/save-from-url", fmt.Sprintf(`{"url":"media://u1/none.png","folderId":%d}`, fid)); rec.Code != http.StatusNotFound {
		t.Fatalf("未登记的媒体 code=%d want 404", rec.Code)
	}
}

func TestLibrarySaveToLibraryChecksProject(t *testing.T) {
	e := newLibraryEnv(t)
	fid := e.mkFolder(t, "f", nil)
	pa, _ := e.projects.Create(LocalUserID, "A", "")
	pb, _ := e.projects.Create(LocalUserID, "B", "")
	aid, _ := e.assets.Insert(assets.AssetRecord{UserID: LocalUserID, ProjectID: pa, ObjectKey: "u1/a.png", SourceKind: "upload",
		URL: "media://u1/a.png", SizeBytes: 3, MimeType: "image/png"})
	body := fmt.Sprintf(`{"folderId":%d,"name":"存档"}`, fid)
	if rec := e.doJSON(http.MethodPost, fmt.Sprintf("/projects/%d/assets/%d/save-to-library", pb, aid), body); rec.Code != http.StatusBadRequest {
		t.Fatalf("别的项目 code=%d want 400", rec.Code)
	}
	rec := e.doJSON(http.MethodPost, fmt.Sprintf("/projects/%d/assets/%d/save-to-library", pa, aid), body)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"name":"存档"`) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLibraryAdminGCDryRunAndRun(t *testing.T) {
	e := newLibraryEnv(t)
	e.storage.PutObject(context.Background(), assets.PutObjectInput{Key: "u1/old.png", Body: []byte("x")})
	e.conn.Exec(`INSERT INTO orphan_objects (object_key, user_id, size_bytes, marked_at) VALUES ('u1/old.png', 1, 1, '2000-01-01 00:00:00')`)
	p, _ := e.storage.Path("u1/old.png")

	rec := e.do(http.MethodPost, "/admin/gc?dryRun=1", nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"count":1`) || !strings.Contains(rec.Body.String(), `"dryRun":true`) {
		t.Fatalf("dryRun code=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("dryRun 不应删文件")
	}
	rec = e.do(http.MethodPost, "/admin/gc", nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":1`) || !strings.Contains(rec.Body.String(), `"totalCandidates":1`) {
		t.Fatalf("gc code=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("过了宽限期且无引用的文件应被删除")
	}
}
