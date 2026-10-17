package http

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// CLI 上传与网页上传共用 readUploadForm（整份读进内存），同样必须先封顶 body；
// 超限是调用方的问题，不能报成可重试的 INTERNAL，否则 CLI 会反复重传同一个大文件。
func TestCLIUploadTooLargeRejectedBeforeStore(t *testing.T) {
	storage := &trackingAssetStorage{}
	ah := NewAssetHandler(storage, &fakeAssetRepo{}, fakeAssetStorageUserRepo{}, NewMediaSigner(storage))
	ah.maxUploadBytes = 64
	h := NewCLIAssetHandler(ah, nil, NewMediaSigner(storage))

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "big.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(bytes.Repeat([]byte("x"), 4096))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/cli/projects/1/assets", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", "1")
	ctx := context.WithValue(withUserID(req.Context(), 1), chi.RouteCtxKey, rctx)
	rec := httptest.NewRecorder()
	h.Upload(rec, req.WithContext(ctx))

	if storage.putCalled {
		t.Fatal("超限不应写存储")
	}
	var body cliErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body=%s: %v", rec.Body.String(), err)
	}
	if rec.Code != http.StatusBadRequest || body.Error.Code != "INVALID_ARGUMENT" || body.Error.Retryable {
		t.Fatalf("status=%d body=%s，want 400 INVALID_ARGUMENT 不可重试", rec.Code, rec.Body.String())
	}
}
