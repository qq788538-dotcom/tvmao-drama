package main

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
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

const e2eMediaToken = "fedcba9876543210fedcba9876543210"

var e2ePNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 24)...)

type e2eEnv struct {
	base    string
	conn    *sql.DB
	storage *assets.LocalStorage
	app     *app
}

// newE2E 用真实的装配函数起一个完整的 sidecar HTTP 服务（只是不经过 stdin 握手）。
func newE2E(t *testing.T) *e2eEnv {
	t.Helper()
	conn := dbtest.Open(t)
	srv := httptest.NewUnstartedServer(nil)
	base := "http://" + srv.Listener.Addr().String()
	dataDir := t.TempDir()
	storage, err := assets.NewLocalStorage(assets.LocalConfig{Root: filepath.Join(dataDir, "media"), BaseURL: base, Token: e2eMediaToken})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "secret.txt"), []byte("API-KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := wire(conn, storage, localauth.Config{Token: testToken, MediaToken: e2eMediaToken, AllowedOrigins: []string{"tauri://localhost"}}, dataDir, "test")
	apphttp.MustWire(a.deps)
	srv.Config.Handler = apphttp.NewRouter(a.deps)
	srv.Start()
	t.Cleanup(srv.Close)
	return &e2eEnv{base: base, conn: conn, storage: storage, app: a}
}

func (e *e2eEnv) call(t *testing.T, method, path string, body io.Reader, contentType string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, e.base+path, body)
	req.Header.Set("Authorization", "Bearer "+testToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func (e *e2eEnv) callJSON(t *testing.T, method, path, body string) (*http.Response, []byte) {
	return e.call(t, method, path, strings.NewReader(body), "application/json")
}

func (e *e2eEnv) upload(t *testing.T, projectID int64, filename string) (id int64, url string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="` + filename + `"`},
		"Content-Type":        {"image/png"},
	})
	part.Write(append(append([]byte{}, e2ePNG...), filename...)) // 内容各不相同，避免去重命中
	mw.Close()
	resp, b := e.call(t, http.MethodPost, fmt.Sprintf("/api/v1/assets?projectId=%d", projectID), &buf, mw.FormDataContentType())
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload %s: code=%d body=%s", filename, resp.StatusCode, b)
	}
	var out struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	json.Unmarshal(b, &out)
	return out.ID, out.URL
}

// 从前端视角走一遍：建项目 → 上传 → 媒体 URL 读回（Range）→ 画布引用 → 快照往返 → 素材库 → 删除项目 → 清理与回收。
func TestE2EProjectUploadCanvasMediaLifecycle(t *testing.T) {
	e := newE2E(t)

	// 1. 建项目
	resp, b := e.callJSON(t, http.MethodPost, "/api/v1/projects", `{"name":"未命名项目"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: %d %s", resp.StatusCode, b)
	}
	var proj struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(b, &proj)

	// 2. 上传（文件名带 Windows 非法字符）
	assetID, mediaURL := e.upload(t, proj.ID, "镜头:1?.png")
	_, tempURL := e.upload(t, proj.ID, "临时.png")

	// 3. 媒体 URL 只带媒体 token，不带接口 token
	if !strings.HasPrefix(mediaURL, e.base+"/media/") || !strings.Contains(mediaURL, "token="+e2eMediaToken) || strings.Contains(mediaURL, testToken) {
		t.Fatalf("mediaURL=%q", mediaURL)
	}

	// 4. <video>/<img> 形态的请求：不带 Authorization，只靠 URL 里的 token；Range 返回 206
	req, _ := http.NewRequest(http.MethodGet, mediaURL, nil)
	req.Header.Set("Range", "bytes=0-7")
	mresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mbody, _ := io.ReadAll(mresp.Body)
	mresp.Body.Close()
	if mresp.StatusCode != http.StatusPartialContent || !bytes.Equal(mbody, e2ePNG[:8]) || mresp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("media: code=%d type=%q body=%q", mresp.StatusCode, mresp.Header.Get("Content-Type"), mbody)
	}

	// 5. 媒体 token 不能调接口（Review Focus 5）
	presp, err := http.Get(e.base + "/api/v1/projects?token=" + e2eMediaToken)
	if err != nil {
		t.Fatal(err)
	}
	presp.Body.Close()
	if presp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("媒体 token 调接口 code=%d want 401", presp.StatusCode)
	}

	// 6. 画布引用：提交本地 URL，落库必须是 media://
	resp, b = e.callJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/canvas/nodes", proj.ID),
		fmt.Sprintf(`{"ownerToken":"t","node":{"id":"n1","type":"image","content":%q}}`, mediaURL))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upsert node: %d %s", resp.StatusCode, b)
	}
	var stored string
	e.conn.QueryRow(`SELECT payload FROM workflow_snapshots WHERE project_id = ?`, proj.ID).Scan(&stored)
	if !strings.Contains(stored, `"media://u1/`) || strings.Contains(stored, "token=") {
		t.Fatalf("落库 payload=%s", stored)
	}
	resp, b = e.call(t, http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/snapshot", proj.ID), nil, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), e.base+"/media/u1/") {
		t.Fatalf("snapshot: %d %s", resp.StatusCode, b)
	}

	// 7. /me
	resp, b = e.call(t, http.MethodGet, "/api/v1/me", nil, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"role":"admin"`) {
		t.Fatalf("me: %d %s", resp.StatusCode, b)
	}

	// 8. 素材库：建目录、按 URL 存入、按 asset 存入（两条都注册在 /library 挂载块之外）
	resp, b = e.callJSON(t, http.MethodPost, "/api/v1/library/folders", `{"name":"素材"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create folder: %d %s", resp.StatusCode, b)
	}
	var folder struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(b, &folder)
	resp, b = e.callJSON(t, http.MethodPost, "/api/v1/library/save-from-url", fmt.Sprintf(`{"url":%q,"folderId":%d}`, mediaURL, folder.ID))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("save-from-url: %d %s", resp.StatusCode, b)
	}
	resp, b = e.callJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/assets/%d/save-to-library", proj.ID, assetID), fmt.Sprintf(`{"folderId":%d}`, folder.ID))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("save-to-library: %d %s", resp.StatusCode, b)
	}

	// 9. 缩略图接口返回原图 URL；reveal 对认不出的 URL 返回 400（不会真的打开访达）
	resp, b = e.callJSON(t, http.MethodPost, "/api/v1/media/thumbs", fmt.Sprintf(`{"urls":[%q]}`, mediaURL))
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), e.base+"/media/u1/") {
		t.Fatalf("thumbs: %d %s", resp.StatusCode, b)
	}
	resp, _ = e.callJSON(t, http.MethodPost, "/api/v1/media/reveal", `{"url":"https://cdn.example/x.png"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("reveal 外部 URL code=%d want 400", resp.StatusCode)
	}

	// 10. 删除项目 → 清理 worker 回收：被素材库引用的文件留下，只在项目里的进孤儿队列
	resp, _ = e.call(t, http.MethodDelete, fmt.Sprintf("/api/v1/projects/%d", proj.ID), nil, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete project code=%d", resp.StatusCode)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.app.cleaner.Start(ctx, 20*time.Millisecond)
	// 媒体 URL 里的 key 是逐段 PathEscape 过的（中文文件名），落库的是原始 key：经 ExtractKey 解回。
	tempKey, ok := e.storage.ExtractKey(tempURL)
	if !ok {
		t.Fatalf("ExtractKey(%q) 认不出", tempURL)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		e.conn.QueryRow(`SELECT COUNT(*) FROM orphan_objects WHERE object_key = ?`, tempKey).Scan(&n)
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("5 秒内项目清理没有把临时文件标成孤儿")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var orphanCount int
	e.conn.QueryRow(`SELECT COUNT(*) FROM orphan_objects`).Scan(&orphanCount)
	if orphanCount != 1 {
		t.Fatalf("孤儿数=%d want 1（素材库还引用着第一张图）", orphanCount)
	}

	// 11. 过了宽限期的孤儿被清扫删除；仍被引用的文件照常可读
	e.conn.Exec(`UPDATE orphan_objects SET marked_at = '2000-01-01 00:00:00'`)
	if res, err := e.app.sweeper.Sweep(context.Background(), library.DefaultGracePeriod); err != nil || res.Deleted != 1 {
		t.Fatalf("sweep res=%+v err=%v", res, err)
	}
	gone, _ := http.Get(tempURL)
	gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Fatalf("被清扫的文件 code=%d want 404", gone.StatusCode)
	}
	kept, _ := http.Get(mediaURL)
	kept.Body.Close()
	if kept.StatusCode != http.StatusOK {
		t.Fatalf("仍被素材库引用的文件 code=%d want 200", kept.StatusCode)
	}
}

// Review Focus 4：经真实路由的穿越尝试（编码后的 ..）一律 404，绝不吐出数据目录里的其他文件。
func TestE2EMediaTraversalIsRejected(t *testing.T) {
	e := newE2E(t)
	for _, p := range []string{"/media/%2e%2e/secret.txt", "/media/..%2fsecret.txt", "/media/u1/%2e%2e/%2e%2e/secret.txt"} {
		resp, err := http.Get(e.base + p + "?token=" + e2eMediaToken)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound || strings.Contains(string(b), "API-KEY") {
			t.Fatalf("%s: code=%d body=%q", p, resp.StatusCode, b)
		}
	}
}

func TestMustWirePanicsOnMissingHandler(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("缺 handler 时 MustWire 应 panic")
		}
	}()
	apphttp.MustWire(apphttp.Deps{})
}

// 前端（P3a）读画布图片像素：媒体 GET 必须带回白名单 Origin 的 CORS 头；ownerToken 固定发 "local"，
// 不先 claim 也要放行。
func TestE2EMediaCORSAndLocalOwnerToken(t *testing.T) {
	e := newE2E(t)
	resp, b := e.callJSON(t, http.MethodPost, "/api/v1/projects", `{"name":"p"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: %d %s", resp.StatusCode, b)
	}
	var proj struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(b, &proj)
	_, mediaURL := e.upload(t, proj.ID, "cors.png")

	req, _ := http.NewRequest(http.MethodGet, mediaURL, nil)
	req.Header.Set("Origin", "tauri://localhost")
	mresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mresp.Body.Close()
	if mresp.StatusCode != http.StatusOK || mresp.Header.Get("Access-Control-Allow-Origin") != "tauri://localhost" {
		t.Fatalf("media CORS: code=%d ACAO=%q", mresp.StatusCode, mresp.Header.Get("Access-Control-Allow-Origin"))
	}

	resp, b = e.callJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/canvas/nodes", proj.ID),
		`{"ownerToken":"local","node":{"id":"n1","type":"text","content":"hi"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ownerToken=local upsert: %d %s", resp.StatusCode, b)
	}
}
