package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

// thumbStubStorage 在 stubStorage 之上把 maxWidth 编进返回 URL，用于验证
// handler 的宽度缺省/夹取逻辑真正传到了存储层（stubStorage 自身忽略宽度）。
type thumbStubStorage struct{ stubStorage }

func (s *thumbStubStorage) SignedThumbURL(_ context.Context, key string, _ time.Duration, maxWidth int) (string, error) {
	return fmt.Sprintf("https://example.test/%s?w=%d", key, maxWidth), nil
}

func postThumbs(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	h := MediaThumbs(NewMediaSigner(&thumbStubStorage{}))
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest("POST", "/media/thumbs", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func decodeThumbs(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var resp struct {
		Thumbs map[string]string `json:"thumbs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, rec.Body.String())
	}
	return resp.Thumbs
}

func TestMediaThumbs_DefaultWidthAndPassthrough(t *testing.T) {
	rec := postThumbs(t, map[string]any{
		"urls": []string{"media://key/a.png", "https://external.example/x.png", ""},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	thumbs := decodeThumbs(t, rec)

	// 我方媒体：换成带缺省宽的缩略图签名 URL。
	if got := thumbs["media://key/a.png"]; got != "https://example.test/key/a.png?w=400" {
		t.Fatalf("expected default-width thumb URL, got %q", got)
	}
	// 外部 URL 不是我方媒体：原样返回，不报错——画布快照里混着外部图是常态。
	if got := thumbs["https://external.example/x.png"]; got != "https://external.example/x.png" {
		t.Fatalf("expected external URL passthrough, got %q", got)
	}
	// 空串跳过，不进结果。
	if _, ok := thumbs[""]; ok {
		t.Fatal("empty url should be skipped")
	}
}

func TestMediaThumbs_WidthClamped(t *testing.T) {
	rec := postThumbs(t, map[string]any{
		"urls":     []string{"media://key/a.png"},
		"maxWidth": 99999,
	})
	thumbs := decodeThumbs(t, rec)
	if got := thumbs["media://key/a.png"]; got != "https://example.test/key/a.png?w=2048" {
		t.Fatalf("expected width clamped to cap, got %q", got)
	}
}

func TestMediaThumbs_TooManyRejected(t *testing.T) {
	urls := make([]string, thumbBatchMax+1)
	for i := range urls {
		urls[i] = fmt.Sprintf("media://key/%d.png", i)
	}
	rec := postThumbs(t, map[string]any{"urls": urls})
	if rec.Code != 400 {
		t.Fatalf("expected 400 for oversized batch, got %d", rec.Code)
	}
}
