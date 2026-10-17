package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/assets"
)

// newTestAssetHandler 组装一个可测的 AssetHandler：fake storage/repo/users/signer，
// probe 默认给一个不会被误用的占位实现（用例按需覆盖）。
func newTestAssetHandler(t *testing.T) *AssetHandler {
	t.Helper()
	h := NewAssetHandler(fakeAssetStorage{}, &fakeAssetRepo{}, fakeAssetStorageUserRepo{}, NewMediaSigner(fakeAssetStorage{}))
	return h
}

// doUpload 用 multipart 构造一次 POST /assets/upload 请求并注入 userID，直接调用
// h.Upload，返回 httptest.ResponseRecorder 供断言状态码。
// 显式走 CreatePart（而非 CreateFormFile）是因为 Upload 靠
// header.Header.Get("Content-Type") 判断视频/图片——CreateFormFile 不带这个头。
func doUpload(t *testing.T, h *AssetHandler, filename, contentType string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="` + filename + `"`},
		"Content-Type":        {contentType},
	}
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/assets/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), 7))

	rec := httptest.NewRecorder()
	h.Upload(rec, req)
	return rec
}

func TestUpload_VideoWritesDurationMs(t *testing.T) {
	var gotDuration int64
	h := newTestAssetHandler(t)
	h.probe = func(ctx context.Context, b []byte) (int64, error) { return 4200, nil }
	h.repo.(*fakeAssetRepo).onInsert = func(rec assets.AssetRecord) { gotDuration = rec.DurationMs }

	rec := doUpload(t, h, "clip.mp4", "video/mp4", []byte("fake-bytes"))
	// Upload 成功写入（非去重命中）返回 201 Created，不是 200——去重命中分支才是 200。
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d", rec.Code)
	}
	if gotDuration != 4200 {
		t.Errorf("duration_ms 落库 = %d, 期望 4200", gotDuration)
	}
}

func TestUpload_ImageSkipsProbe(t *testing.T) {
	probed := false
	h := newTestAssetHandler(t)
	h.probe = func(ctx context.Context, b []byte) (int64, error) { probed = true; return 1, nil }
	doUpload(t, h, "a.png", "image/png", []byte("img"))
	if probed {
		t.Error("图片不应调用 probe")
	}
}

// 桌面端探测失败只是不记时长，上传照常成功（不计费；用户机器上没有 ffprobe）。
func TestUpload_ProbeFailureStillStoresWithoutDuration(t *testing.T) {
	gotDuration := int64(-1)
	h := newTestAssetHandler(t)
	h.probe = func(ctx context.Context, b []byte) (int64, error) { return 0, errors.New("ffprobe unavailable") }
	h.repo.(*fakeAssetRepo).onInsert = func(rec assets.AssetRecord) { gotDuration = rec.DurationMs }
	rec := doUpload(t, h, "clip.mp4", "video/mp4", []byte("v"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if gotDuration != 0 {
		t.Fatalf("探测失败应不记时长，duration_ms=%d", gotDuration)
	}
}

// 默认探测器是纯 Go 可用的 Portable：没有 ffprobe 也能给上传的 mp4 记下 duration_ms，
// 后处理就不必再下载探测。
func TestUpload_DefaultProbeRecordsMP4DurationWithoutFFprobe(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var gotDuration int64
	h := newTestAssetHandler(t)
	h.repo.(*fakeAssetRepo).onInsert = func(rec assets.AssetRecord) { gotDuration = rec.DurationMs }
	if rec := doUpload(t, h, "clip.mp4", "video/mp4", postprocessMP4(6100)); rec.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if gotDuration != 6100 {
		t.Fatalf("duration_ms = %d, want 6100", gotDuration)
	}
}

func TestUpload_DefaultProbeUnparseableVideoStillStores(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h := newTestAssetHandler(t) // 默认探测器读不出时长：照常上传，只是不记时长
	if rec := doUpload(t, h, "clip.mp4", "video/mp4", []byte("v")); rec.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
}

// sidecar 前面没有 nginx 挡 body 大小，超限必须在读进内存之前拒绝，且不写存储。
func TestUpload_TooLargeReturns413(t *testing.T) {
	storage := &trackingAssetStorage{}
	h := NewAssetHandler(storage, &fakeAssetRepo{}, fakeAssetStorageUserRepo{}, NewMediaSigner(storage))
	h.maxUploadBytes = 64
	rec := doUpload(t, h, "big.png", "image/png", bytes.Repeat([]byte("x"), 1024))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d body=%s，want 413", rec.Code, rec.Body.String())
	}
	if storage.putCalled {
		t.Error("超限不应写存储")
	}
}

// 存储配额预检：used+size > quota 时 409 + 中文文案（前端 uploadIntoNode 透出
// response.data.error），且完全不触碰对象存储。
func TestUpload_StorageQuotaExceededRejects409(t *testing.T) {
	storage := &trackingAssetStorage{}
	// used 100 / quota 101：再传 5 字节即超额。
	h := NewAssetHandler(storage, &fakeAssetRepo{}, fakeAssetStorageUserRepo{used: 100, quota: 101}, NewMediaSigner(storage))
	rec := doUpload(t, h, "a.png", "image/png", []byte("12345"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("超配额应 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if m["error"] != "存储空间已满，请清理后重试" {
		t.Fatalf("409 body 应为固定中文文案, got %v", m)
	}
	if storage.putCalled {
		t.Error("超配额不应调用 PutObject")
	}
}

// 配额闸必须在去重判定之后：dedup 命中不新占存储，即使账面已满也要放行——
// 否则用户重复拖同一个文件反而被报「空间已满」。
func TestUpload_DedupHitBypassesQuotaGate(t *testing.T) {
	h := NewAssetHandler(fakeAssetStorage{}, dedupAssetRepo{&fakeAssetRepo{}}, fakeAssetStorageUserRepo{used: 999, quota: 100}, NewMediaSigner(fakeAssetStorage{}))
	rec := doUpload(t, h, "a.png", "image/png", []byte("bytes"))
	if rec.Code != http.StatusOK {
		t.Fatalf("去重命中应无视配额返回 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// trackingAssetStorage 包装 fakeAssetStorage，记录 PutObject 是否被调用过，
// 用于断言探测失败路径完全不触碰存储。
type trackingAssetStorage struct {
	fakeAssetStorage
	putCalled bool
}

func (s *trackingAssetStorage) PutObject(ctx context.Context, input assets.PutObjectInput) (assets.StoredObject, error) {
	s.putCalled = true
	return s.fakeAssetStorage.PutObject(ctx, input)
}

// dedupAssetRepo 让 FindByUserProjectHash 恒命中，用来验证去重分支的响应形状。
type dedupAssetRepo struct{ *fakeAssetRepo }

func (dedupAssetRepo) FindByUserProjectHash(int64, int64, string) (*assets.AssetRecord, error) {
	return &assets.AssetRecord{ID: 77, URL: "https://example.com/old.png", SizeBytes: 10, MimeType: "image/png"}, nil
}

// 网页素材上传的响应契约在「提取 storeUpload 供 CLI 复用」之后必须逐字不变——
// 前端 uploadAsset 直接读这几个字段，多一个少一个都可能让画布上传静默出错。
func TestWebAssetUploadResponseShapeUnchangedAfterExtract(t *testing.T) {
	h := newTestAssetHandler(t)
	rec := doUpload(t, h, "a.png", "image/png", []byte("bytes"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("新建应 201，got %d body=%s", rec.Code, rec.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"id", "url", "size", "mimeType"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("响应缺字段 %s: %v", k, m)
		}
	}
	// 改造前新建分支不带 deduped，多出来会让前端把新图当成去重命中。
	if _, ok := m["deduped"]; ok {
		t.Fatalf("新建响应不该带 deduped: %v", m)
	}
}

func TestWebAssetUploadDedupBranchStillReturns200(t *testing.T) {
	h := NewAssetHandler(fakeAssetStorage{}, dedupAssetRepo{&fakeAssetRepo{}}, fakeAssetStorageUserRepo{}, NewMediaSigner(fakeAssetStorage{}))
	rec := doUpload(t, h, "a.png", "image/png", []byte("bytes"))
	if rec.Code != http.StatusOK {
		t.Fatalf("去重命中应 200，got %d body=%s", rec.Code, rec.Body.String())
	}
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if m["deduped"] != true || m["id"] != float64(77) {
		t.Fatalf("去重响应 = %v", m)
	}
}
