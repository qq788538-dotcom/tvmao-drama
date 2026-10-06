package http

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/users"
)

var mediaPNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 24)...)

type mediaEnv struct {
	conn      *sql.DB
	storage   *assets.LocalStorage
	handler   *MediaHandler
	revealed  []string
	revealErr error
	dataDir   string
}

// newMediaEnv 的目录布局模拟真实数据目录：<dataDir>/media 是媒体根，<dataDir>/secret.txt 代表 data.db。
func newMediaEnv(t *testing.T) *mediaEnv {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "Application Support", "com.miaoju.desktop")
	storage, err := assets.NewLocalStorage(assets.LocalConfig{
		Root: filepath.Join(dataDir, "media"), BaseURL: "http://127.0.0.1:5555", Token: "mt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "secret.txt"), []byte("API-KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := &mediaEnv{conn: dbtest.Open(t), storage: storage, dataDir: dataDir}
	env.handler = NewMediaHandler(storage, NewMediaSigner(storage), NewSQLMediaMimeStore(env.conn), func(p string) error {
		env.revealed = append(env.revealed, p)
		return env.revealErr
	})
	return env
}

func (e *mediaEnv) put(t *testing.T, key string, body []byte, assetMime string) {
	t.Helper()
	if _, err := e.storage.PutObject(t.Context(), assets.PutObjectInput{Key: key, Body: body}); err != nil {
		t.Fatal(err)
	}
	if assetMime != "" {
		if _, err := e.conn.Exec(`INSERT INTO assets (user_id, object_key, url, size_bytes, mime_type) VALUES (1, ?, ?, ?, ?)`,
			key, "media://"+key, len(body), assetMime); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *mediaEnv) serve(method, target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.handler.Serve(rec, req)
	return rec
}

func (e *mediaEnv) reveal(body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/reveal", strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.handler.Reveal(rec, req)
	return rec
}

// <video> 拖进度条、mediabunny 抽帧都靠 Range；没有 206 就只能从头播。
func TestMediaServeRangeAndHead(t *testing.T) {
	env := newMediaEnv(t)
	env.put(t, "u1/2026/10/v.mp4", []byte("0123456789"), "video/mp4")

	rec := env.serve("GET", "/media/u1/2026/10/v.mp4", map[string]string{"Range": "bytes=2-5"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "2345" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	h := rec.Header()
	if h.Get("Content-Range") != "bytes 2-5/10" || h.Get("Content-Type") != "video/mp4" ||
		h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("headers=%v", h)
	}
	head := env.serve("HEAD", "/media/u1/2026/10/v.mp4", nil)
	if head.Code != http.StatusOK || head.Header().Get("Content-Length") != "10" || head.Body.Len() != 0 {
		t.Fatalf("HEAD code=%d len=%q body=%d", head.Code, head.Header().Get("Content-Length"), head.Body.Len())
	}
}

// 生成产物的 key 没有扩展名（gen/task-<id>-<i>），Content-Type 只能来自库里的 mime_type 或嗅探。
func TestMediaServeContentTypeForExtensionlessKeys(t *testing.T) {
	env := newMediaEnv(t)
	env.put(t, "u1/gen_task-1-0", []byte("not really an image"), "image/webp")
	env.put(t, "u1/gen_task-2-0", mediaPNG, "")
	env.put(t, "u1/gen_task-3-0", mediaPNG, "application/octet-stream")
	env.put(t, "library/1/voice", []byte("ID3fake"), "")
	res, _ := env.conn.Exec(`INSERT INTO library_folders (user_id, name) VALUES (1, 'f')`)
	fid, _ := res.LastInsertId()
	env.conn.Exec(`INSERT INTO library_items (user_id, folder_id, object_key, url, size_bytes, mime_type, kind)
		VALUES (1, ?, 'library/1/voice', 'media://library/1/voice', 7, 'audio/mpeg', 'audio')`, fid)

	cases := map[string]string{
		"/media/u1/gen_task-1-0": "image/webp", // assets.mime_type 优先
		"/media/u1/gen_task-2-0": "image/png",  // 库里没有 → 嗅探
		"/media/u1/gen_task-3-0": "image/png",  // octet-stream 视为未知 → 嗅探
		"/media/library/1/voice": "audio/mpeg", // 回落 library_items.mime_type
	}
	for target, want := range cases {
		rec := env.serve("GET", target, nil)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != want {
			t.Errorf("%s: code=%d type=%q want %q", target, rec.Code, rec.Header().Get("Content-Type"), want)
		}
	}
}

// media/ 的上一级就是 data.db：任何逃出媒体根目录的路径都必须 404，且绝不能吐出内容。
func TestMediaServeRejectsTraversalAndMissing(t *testing.T) {
	env := newMediaEnv(t)
	os.MkdirAll(filepath.Join(env.storage.Root(), "u1"), 0o700)
	for _, target := range []string{
		"/media/../secret.txt", "/media/%2e%2e/secret.txt", "/media/u1/..%2f..%2fsecret.txt",
		"/media/", "/media/u1", "/media/u1/missing.png", "/media/u1/a.png%00",
	} {
		rec := env.serve("GET", target, nil)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "API-KEY") {
			t.Errorf("%s: code=%d body=%q，want 404", target, rec.Code, rec.Body.String())
		}
	}
}

func TestMediaRevealOpensLocalPath(t *testing.T) {
	env := newMediaEnv(t)
	env.put(t, "u1/2026/10/a b.png", mediaPNG, "image/png")
	signed := NewMediaSigner(env.storage).Sign("media://u1/2026/10/a b.png")
	want, _ := env.storage.Path("u1/2026/10/a b.png")
	for _, u := range []string{signed, "media://u1/2026/10/a b.png", "http://127.0.0.1:40000/media/u1/2026/10/a%20b.png?token=old"} {
		env.revealed = nil
		body, _ := json.Marshal(map[string]string{"url": u})
		rec := env.reveal(string(body))
		if rec.Code != http.StatusNoContent || len(env.revealed) != 1 || env.revealed[0] != want {
			t.Fatalf("url=%s code=%d revealed=%v want %s", u, rec.Code, env.revealed, want)
		}
	}
}

func TestMediaRevealRejectsUnknownAndMissing(t *testing.T) {
	env := newMediaEnv(t)
	cases := map[string]int{
		`{"url":"https://cdn.example/a.png"}`: http.StatusBadRequest,
		`{"url":"media://../secret.txt"}`:     http.StatusBadRequest,
		`{"url":"media://u1/missing.png"}`:    http.StatusNotFound,
		`{"url":""}`:                          http.StatusBadRequest,
		`not json`:                            http.StatusBadRequest,
	}
	for body, want := range cases {
		if rec := env.reveal(body); rec.Code != want {
			t.Errorf("%s: code=%d want %d", body, rec.Code, want)
		}
	}
	if len(env.revealed) != 0 {
		t.Fatalf("非法请求不应打开文件夹，revealed=%v", env.revealed)
	}
}

func TestMediaRevealReportsFailure(t *testing.T) {
	env := newMediaEnv(t)
	env.put(t, "u1/a.png", mediaPNG, "image/png")
	env.revealErr = errors.New("open failed")
	if rec := env.reveal(`{"url":"media://u1/a.png"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("code=%d want 500", rec.Code)
	}
}

// Review Focus 1：Windows 非法字符的文件名上传后，用返回的 URL 能原样读回。
func TestMediaUploadedFileWithIllegalNameIsServable(t *testing.T) {
	env := newMediaEnv(t)
	signer := NewMediaSigner(env.storage)
	ah := NewAssetHandler(env.storage, assets.NewSQLiteRepository(env.conn), users.NewSQLiteRepository(env.conn), signer)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="镜头:1?.png"`},
		"Content-Type":        {"image/png"},
	})
	part.Write(mediaPNG)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assets", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), LocalUserID))
	rec := httptest.NewRecorder()
	ah.Upload(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		URL string `json:"url"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if !strings.HasPrefix(out.URL, "http://127.0.0.1:5555/media/u1/") {
		t.Fatalf("url=%q", out.URL)
	}
	path := strings.TrimPrefix(strings.SplitN(out.URL, "?", 2)[0], "http://127.0.0.1:5555")
	got := env.serve("GET", path, nil)
	body, _ := io.ReadAll(got.Body)
	if got.Code != http.StatusOK || !bytes.Equal(body, mediaPNG) || got.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("serve code=%d type=%q len=%d", got.Code, got.Header().Get("Content-Type"), len(body))
	}
}
