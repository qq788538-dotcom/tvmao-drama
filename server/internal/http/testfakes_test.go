package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/projects"
)

// ─── 存储替身 ────────────────────────────────────────────────────────────────

// stubStorage 统计 SignedURL 的调用次数；其他 Storage 方法都是空 stub。
type stubStorage struct {
	signCalls atomic.Int64
}

func (s *stubStorage) PutObject(context.Context, assets.PutObjectInput) (assets.StoredObject, error) {
	return assets.StoredObject{}, errors.New("not used")
}
func (s *stubStorage) DeleteObject(context.Context, string) error { return nil }
func (s *stubStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	n := s.signCalls.Add(1)
	return fmt.Sprintf("https://example.test/%s?sig=%d", key, n), nil
}
func (s *stubStorage) SignedThumbURL(_ context.Context, key string, _ time.Duration, _ int) (string, error) {
	n := s.signCalls.Add(1)
	return fmt.Sprintf("https://example.test/%s?sig=%d", key, n), nil
}
func (s *stubStorage) ExtractKey(urlOrKey string) (string, bool) {
	// 任何以 "media://" 开头的都识别为我们自己的。
	const prefix = "media://"
	if len(urlOrKey) > len(prefix) && urlOrKey[:len(prefix)] == prefix {
		return urlOrKey[len(prefix):], true
	}
	return "", false
}
func (s *stubStorage) StaticKey(userID int64, filename string) string {
	return s.NewKey(userID, filename)
}
func (s *stubStorage) NewKey(int64, string) string { return "" }
func (s *stubStorage) GetObject(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (s *stubStorage) CopyObject(context.Context, string, string, string) (assets.StoredObject, error) {
	return assets.StoredObject{}, errors.New("not used")
}

// fakeAssetStorage 是 assets.Storage 的手写 fake，PutObject 直接回声成功，不落盘。
type fakeAssetStorage struct{}

func (fakeAssetStorage) PutObject(_ context.Context, input assets.PutObjectInput) (assets.StoredObject, error) {
	_, size := input.Content()
	return assets.StoredObject{URL: "https://example.com/" + input.Key, Size: size}, nil
}
func (fakeAssetStorage) DeleteObject(context.Context, string) error { return nil }
func (fakeAssetStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return key, nil
}
func (fakeAssetStorage) SignedThumbURL(_ context.Context, key string, _ time.Duration, _ int) (string, error) {
	return key, nil
}
func (fakeAssetStorage) ExtractKey(urlOrKey string) (string, bool) { return urlOrKey, true }
func (fakeAssetStorage) GetObject(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (fakeAssetStorage) StaticKey(userID int64, filename string) string { return filename }
func (fakeAssetStorage) NewKey(userID int64, filename string) string    { return filename }
func (fakeAssetStorage) CopyObject(_ context.Context, _, dstKey, _ string) (assets.StoredObject, error) {
	return assets.StoredObject{URL: dstKey}, nil
}

// ─── assets / users 仓库替身 ─────────────────────────────────────────────────

// fakeAssetRepo 是 assets.Repository 的手写 fake；onInsert 钩子让测试观察 Insert 收到的记录。
type fakeAssetRepo struct {
	nextID   int64
	onInsert func(rec assets.AssetRecord)
}

func (f *fakeAssetRepo) Insert(rec assets.AssetRecord) (int64, error) {
	if f.onInsert != nil {
		f.onInsert(rec)
	}
	f.nextID++
	return f.nextID, nil
}
func (f *fakeAssetRepo) ListForUser(int64, int) ([]assets.AssetRecord, error) { return nil, nil }
func (f *fakeAssetRepo) ListForProject(int64) ([]assets.AssetRecord, error)   { return nil, nil }
func (f *fakeAssetRepo) DeleteForProject(int64) (int64, error)                { return 0, nil }
func (f *fakeAssetRepo) ListForTask(int64) ([]assets.AssetRecord, error)      { return nil, nil }
func (f *fakeAssetRepo) DeleteForTask(int64) (int64, error)                   { return 0, nil }
func (f *fakeAssetRepo) ListByObjectKey(string) ([]assets.AssetRecord, error) { return nil, nil }
func (f *fakeAssetRepo) ClearLibraryRef(int64) error                          { return nil }
func (f *fakeAssetRepo) FindByUserProjectHash(int64, int64, string) (*assets.AssetRecord, error) {
	// 总是走"未命中"分支，让 Upload 走真实写入路径而不是去重短路。
	return nil, sql.ErrNoRows
}
func (f *fakeAssetRepo) FindByTaskSlot(int64, int) (*assets.AssetRecord, error) {
	return nil, sql.ErrNoRows
}
func (f *fakeAssetRepo) InsertSlotTx(*sql.Tx, assets.AssetRecord, int) (int64, bool, error) {
	return 0, false, nil
}
func (f *fakeAssetRepo) GetDurationMsByObjectKey(string) (int64, bool, error) { return 0, false, nil }
func (f *fakeAssetRepo) SetDurationMsByObjectKey(string, int64) error         { return nil }

// fakeAssetStorageUserRepo 是 storageUserRepo 的手写 fake。零值 = 配额充裕（quota 1GB）。
type fakeAssetStorageUserRepo struct {
	used, quota int64
}

func (fakeAssetStorageUserRepo) AddStorage(int64, int64) error { return nil }
func (f fakeAssetStorageUserRepo) StorageUsage(int64) (int64, int64, error) {
	if f.quota == 0 {
		return f.used, 1 << 30, nil
	}
	return f.used, f.quota, nil
}

// ─── projects 仓库替身与请求构造 ─────────────────────────────────────────────

type fakeProjectRepository struct {
	project        *projects.Project
	snapshot       *projects.Snapshot
	mutatePayload  json.RawMessage
	mutatedPayload json.RawMessage
	mutateVersion  int64
	mutateErr      error
	mutateCalled   bool
}

func (f *fakeProjectRepository) Create(int64, string, string) (int64, error) { return 0, nil }
func (f *fakeProjectRepository) ListForUser(int64, int, int) ([]projects.Project, int, error) {
	return nil, 0, nil
}
func (f *fakeProjectRepository) GetForUser(_, _ int64) (*projects.Project, error) {
	if f.project == nil {
		return nil, projects.ErrNotFound
	}
	return f.project, nil
}
func (f *fakeProjectRepository) Update(int64, int64, string, string) error { return nil }
func (f *fakeProjectRepository) SetCover(int64, int64, string) error       { return nil }
func (f *fakeProjectRepository) Touch(int64, int64) error                  { return nil }
func (f *fakeProjectRepository) Delete(int64, int64) error                 { return nil }
func (f *fakeProjectRepository) SaveAuthoritative(int64, json.RawMessage) (int64, error) {
	return 0, nil
}
func (f *fakeProjectRepository) MutateSnapshot(int64, func(json.RawMessage) (json.RawMessage, bool, error)) (int64, error) {
	return 0, nil
}
func (f *fakeProjectRepository) MutateClientSnapshot(_ int64, fn func(json.RawMessage) (json.RawMessage, bool, error)) (int64, error) {
	f.mutateCalled = true
	if f.mutateErr != nil {
		return 0, f.mutateErr
	}
	next, changed, err := fn(f.mutatePayload)
	if err != nil {
		return 0, err
	}
	if changed {
		f.mutatedPayload = next
	}
	if f.mutateVersion != 0 {
		return f.mutateVersion, nil
	}
	return 1, nil
}
func (f *fakeProjectRepository) GetLatest(int64) (*projects.Snapshot, error) { return f.snapshot, nil }
func (f *fakeProjectRepository) ListPendingDeletion(int) ([]projects.PendingProject, error) {
	return nil, nil
}
func (f *fakeProjectRepository) HardDelete(int64) error { return nil }

func newProjectSnapshotRequest(t *testing.T, method string, body []byte) *http.Request {
	t.Helper()
	if body == nil {
		body = []byte{}
	}
	req := httptest.NewRequest(method, "/api/v1/projects/42/snapshot", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", "42")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = withProjectID(ctx, 42)
	return req.WithContext(withUserID(ctx, 7))
}

func newProjectCanvasRequest(t *testing.T, method string, path string, body []byte, params map[string]string) *http.Request {
	t.Helper()
	if body == nil {
		body = []byte(`{}`)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", "42")
	for key, value := range params {
		rctx.URLParams.Add(key, value)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = withProjectID(ctx, 42)
	return req.WithContext(withUserID(ctx, 7))
}

// withTestUserID 通过请求 context 注入一个 uid，让 handler 的 userIDFromContext 能看到调用方。
func withTestUserID(req *http.Request, uid int64) *http.Request {
	ctx := context.WithValue(req.Context(), userIDContextKey, uid)
	return req.WithContext(ctx)
}
