package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/directormodels"
	"github.com/james/huabu_studio/apps/api/internal/library"
)

// directorFakeRepo 是 directormodels.Repository 的手写 fake：条数恒 0（不触发 limit_reached）。
type directorFakeRepo struct{}

func (directorFakeRepo) Insert(directormodels.Model) (int64, error)       { return 1, nil }
func (directorFakeRepo) ListByUser(int64) ([]directormodels.Model, error) { return nil, nil }
func (directorFakeRepo) GetByID(int64) (*directormodels.Model, error) {
	return nil, directormodels.ErrNotFound
}
func (directorFakeRepo) Delete(directormodels.Execer, int64, int64) error { return nil }
func (directorFakeRepo) CountByUser(int64) (int64, error)                 { return 0, nil }
func (directorFakeRepo) CountProjectsReferencing(int64, string) (int64, error) {
	return 0, nil
}

// directorTrackingStorage 实现 assets.Storage，记录是否写过对象。
type directorTrackingStorage struct{ putCalled bool }

func (s *directorTrackingStorage) PutObject(_ context.Context, in assets.PutObjectInput) (assets.StoredObject, error) {
	s.putCalled = true
	_, size := in.Content()
	return assets.StoredObject{URL: "media://" + in.Key, Size: size}, nil
}
func (s *directorTrackingStorage) DeleteObject(context.Context, string) error { return nil }
func (s *directorTrackingStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://cdn.example.com/" + key, nil
}
func (s *directorTrackingStorage) SignedThumbURL(_ context.Context, key string, _ time.Duration, _ int) (string, error) {
	return "https://cdn.example.com/" + key, nil
}
func (s *directorTrackingStorage) ExtractKey(u string) (string, bool) {
	if strings.HasPrefix(u, "media://") {
		return strings.TrimPrefix(u, "media://"), true
	}
	return "", false
}
func (s *directorTrackingStorage) GetObject(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (s *directorTrackingStorage) StaticKey(_ int64, filename string) string { return "k/" + filename }
func (s *directorTrackingStorage) NewKey(_ int64, filename string) string    { return "k/" + filename }
func (s *directorTrackingStorage) CopyObject(_ context.Context, _, dstKey, _ string) (assets.StoredObject, error) {
	return assets.StoredObject{URL: "media://" + dstKey}, nil
}

type directorFakeUserRepo struct {
	used, quota int64
	added       []int64
}

func (f *directorFakeUserRepo) AddStorage(_ int64, delta int64) error {
	f.added = append(f.added, delta)
	return nil
}
func (f *directorFakeUserRepo) StorageUsage(int64) (int64, int64, error) { return f.used, f.quota, nil }

func doDirectorUpload(t *testing.T, h *DirectorModelHandler, filename string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="` + filename + `"`},
		"Content-Type":        {"application/octet-stream"},
	})
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/director/models", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), 7))
	rec := httptest.NewRecorder()
	h.Upload(rec, req)
	return rec
}

// 存储配额预检：超额 409 + 中文文案，不触碰存储（区别于条数上限的 {"error":"limit_reached"}）。
func TestDirectorModelUpload_StorageQuotaExceededRejects409(t *testing.T) {
	storage := &directorTrackingStorage{}
	h := NewDirectorModelHandler(&directormodels.Service{Repo: directorFakeRepo{}}, storage, nil,
		&directorFakeUserRepo{used: 100, quota: 101}, directorFakeSigner{}, nil)
	rec := doDirectorUpload(t, h, "model.fbx", []byte("12345"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("超配额应 409, got %d body=%s", rec.Code, rec.Body)
	}
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if m["error"] != "存储空间已满，请清理后重试" {
		t.Fatalf("409 body = %v", m)
	}
	if storage.putCalled {
		t.Error("超配额不应写对象")
	}
}

func TestDirectorModelUpload_UnderQuotaPasses(t *testing.T) {
	storage := &directorTrackingStorage{}
	users := &directorFakeUserRepo{used: 10, quota: 1000}
	h := NewDirectorModelHandler(&directormodels.Service{Repo: directorFakeRepo{}}, storage, nil, users, directorFakeSigner{}, nil)
	rec := doDirectorUpload(t, h, "model.obj", []byte("obj-bytes"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("未超额应 201, got %d body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"url":"https://signed/`) || len(users.added) != 1 || users.added[0] != 9 {
		t.Fatalf("body=%s added=%v", rec.Body, users.added)
	}
}

func TestDirectorModelUpload_RejectsUnsupportedExt(t *testing.T) {
	h := NewDirectorModelHandler(&directormodels.Service{Repo: directorFakeRepo{}}, &directorTrackingStorage{}, nil,
		&directorFakeUserRepo{quota: 1 << 40}, directorFakeSigner{}, nil)
	if rec := doDirectorUpload(t, h, "scene.gltf", []byte("x")); rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", rec.Code)
	}
}

func directorDeleteReq(id string, userID int64, force bool) *http.Request {
	target := "/api/v1/director/models/" + id
	if force {
		target += "?force=1"
	}
	req := httptest.NewRequest(http.MethodDelete, target, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("modelID", id)
	ctx := context.WithValue(withUserID(req.Context(), userID), chi.RouteCtxKey, rctx)
	return req.WithContext(ctx)
}

// 被画布引用的模型默认拦下；force=1 后删行、进孤儿管道（7 天宽限）、释放配额。
func TestDirectorModelDelete_ReferencedNeedsForce(t *testing.T) {
	conn := dbtest.Open(t)
	repo := directormodels.NewSQLiteRepository(conn)
	id, err := repo.Insert(directormodels.Model{UserID: 1, Name: "robot", FileName: "robot.fbx", ObjectKey: "u1/ab_robot.fbx", SizeBytes: 42, MimeType: "x"})
	if err != nil {
		t.Fatal(err)
	}
	res, _ := conn.Exec(`INSERT INTO projects (user_id, name) VALUES (1, 'p')`)
	pid, _ := res.LastInsertId()
	if _, err := conn.Exec(`INSERT INTO workflow_snapshots (project_id, payload) VALUES (?, ?)`, pid, `{"n":"media://u1/ab_robot.fbx"}`); err != nil {
		t.Fatal(err)
	}
	users := &directorFakeUserRepo{quota: 1 << 40}
	h := NewDirectorModelHandler(&directormodels.Service{Repo: repo}, &directorTrackingStorage{},
		library.NewSQLiteOrphansRepository(conn), users, directorFakeSigner{}, conn)
	idStr := strconv.FormatInt(id, 10)

	rec := httptest.NewRecorder()
	h.Delete(rec, directorDeleteReq(idStr, 1, false))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"referencedProjects":1`) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	h.Delete(rec, directorDeleteReq(idStr, 1, true))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("force 删除 code=%d body=%s", rec.Code, rec.Body)
	}
	if _, err := repo.GetByID(id); err == nil {
		t.Fatal("行应已删除")
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM orphan_objects WHERE object_key = 'u1/ab_robot.fbx'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("应进孤儿管道: n=%d err=%v", n, err)
	}
	if len(users.added) != 1 || users.added[0] != -42 {
		t.Fatalf("应释放配额: %v", users.added)
	}
}

func TestDirectorModelDelete_OtherUser404(t *testing.T) {
	conn := dbtest.Open(t)
	repo := directormodels.NewSQLiteRepository(conn)
	id, _ := repo.Insert(directormodels.Model{UserID: 1, Name: "m", FileName: "m.fbx", ObjectKey: "u1/m.fbx", SizeBytes: 1, MimeType: "x"})
	h := NewDirectorModelHandler(&directormodels.Service{Repo: repo}, &directorTrackingStorage{},
		library.NewSQLiteOrphansRepository(conn), &directorFakeUserRepo{}, directorFakeSigner{}, conn)
	rec := httptest.NewRecorder()
	h.Delete(rec, directorDeleteReq(strconv.FormatInt(id, 10), 2, true))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d", rec.Code)
	}
	if _, err := repo.GetByID(id); err != nil {
		t.Fatal("别人的模型不应被删")
	}
}

// 请求体上限：超过 30MB+multipart 开销必须在读表单阶段就 413，而不是读完整个请求再 400。
func TestDirectorModelUpload_OversizeBodyRejects413(t *testing.T) {
	storage := &directorTrackingStorage{}
	h := NewDirectorModelHandler(&directormodels.Service{Repo: directorFakeRepo{}}, storage, nil,
		&directorFakeUserRepo{quota: 1 << 40}, directorFakeSigner{}, nil)
	rec := doDirectorUpload(t, h, "big.glb", make([]byte, 33<<20))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大请求体应 413, got %d body=%s", rec.Code, rec.Body)
	}
	if storage.putCalled {
		t.Error("超限不应写对象")
	}
}
