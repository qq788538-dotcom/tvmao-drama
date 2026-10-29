package http

import (
	"bytes"
	"image"
	"image/jpeg"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
)

func mediaJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newThumbMediaEnv(t *testing.T) *mediaEnv {
	t.Helper()
	env := newMediaEnv(t)
	tc, err := assets.NewThumbCache(filepath.Join(env.dataDir, "cache", "thumbs"))
	if err != nil {
		t.Fatal(err)
	}
	env.handler.WithThumbs(tc)
	return env
}

// ?w= 出缩略图：像素变小、响应头与原图同一套（nosniff/CSP/缓存），Range 也照常。
func TestMediaServeThumb(t *testing.T) {
	env := newThumbMediaEnv(t)
	orig := mediaJPEG(t, 1600, 800)
	env.put(t, "u1/gen_task-9-0", orig, "image/jpeg")

	rec := env.serve("GET", "/media/u1/gen_task-9-0?w=400&token=mt", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("code=%d type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
	if err != nil || cfg.Width != 400 || cfg.Height != 200 {
		t.Fatalf("缩略图尺寸不对: %+v %v", cfg, err)
	}
	h := rec.Header()
	if h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || h.Get("Cache-Control") == "" {
		t.Fatalf("缩略图响应头应与原图一致: %v", h)
	}
	if rec.Body.Len() >= len(orig) {
		t.Fatalf("缩略图应比原图小: %d >= %d", rec.Body.Len(), len(orig))
	}

	// 不带 w、w 非法：原图字节。
	for _, target := range []string{"/media/u1/gen_task-9-0", "/media/u1/gen_task-9-0?w=abc", "/media/u1/gen_task-9-0?w=0", "/media/u1/gen_task-9-0?w=99999"} {
		if rec := env.serve("GET", target, nil); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), orig) {
			t.Errorf("%s: 应给原图 code=%d len=%d", target, rec.Code, rec.Body.Len())
		}
	}
	if rec := env.serve("GET", "/media/u1/gen_task-9-0?w=400", map[string]string{"Range": "bytes=0-1"}); rec.Code != http.StatusPartialContent || rec.Body.Len() != 2 {
		t.Fatalf("缩略图也支持 Range: code=%d len=%d", rec.Code, rec.Body.Len())
	}
}

// 不支持的格式（gif/webp/视频…）回退原图，Content-Type 仍按原图走。
func TestMediaServeThumbFallsBackForUnsupported(t *testing.T) {
	env := newThumbMediaEnv(t)
	body := []byte("GIF89a not decoded")
	env.put(t, "u1/anim.gif", body, "image/gif")
	rec := env.serve("GET", "/media/u1/anim.gif?w=400", nil)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), body) || rec.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("应回退原图: code=%d type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

// 原图删了：缩略图缓存还在也必须 404，不能借缓存把已删的文件继续发出去。
func TestMediaServeThumbRequiresOriginal(t *testing.T) {
	env := newThumbMediaEnv(t)
	env.put(t, "u1/a.jpg", mediaJPEG(t, 800, 400), "")
	if rec := env.serve("GET", "/media/u1/a.jpg?w=100", nil); rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if err := env.storage.DeleteObject(t.Context(), "u1/a.jpg"); err != nil {
		t.Fatal(err)
	}
	if rec := env.serve("GET", "/media/u1/a.jpg?w=100", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("原图已删应 404, got %d", rec.Code)
	}
}

// 未启用缩略图（缓存目录建不起来）：带 w 也照常给原图。
func TestMediaServeWithoutThumbsIgnoresW(t *testing.T) {
	env := newMediaEnv(t)
	orig := mediaJPEG(t, 800, 400)
	env.put(t, "u1/a.jpg", orig, "")
	if rec := env.serve("GET", "/media/u1/a.jpg?w=100", nil); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), orig) {
		t.Fatalf("应给原图: code=%d", rec.Code)
	}
}
