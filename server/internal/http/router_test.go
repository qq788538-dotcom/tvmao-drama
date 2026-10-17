package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/localauth"
)

const testToken = "0123456789abcdef0123456789abcdef"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewRouter(Deps{
		Auth:    localauth.Config{Token: testToken, AllowedOrigins: []string{"tauri://localhost"}},
		DataDir: t.TempDir(),
		Version: "test",
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPingRequiresToken(t *testing.T) {
	srv := newTestServer(t)
	resp, err := nethttp.Get(srv.URL + "/api/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != nethttp.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", resp.StatusCode)
	}
}

func TestPing(t *testing.T) {
	srv := newTestServer(t)
	resp, err := nethttp.Get(srv.URL + "/api/v1/ping?token=" + testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Version != "test" {
		t.Fatalf("body = %+v", body)
	}
}

// 预检请求打到只注册了 GET 的路径上，也必须拿到 204 而不是 405。
func TestPreflightOnGetOnlyRoute(t *testing.T) {
	srv := newTestServer(t)
	req, _ := nethttp.NewRequest("OPTIONS", srv.URL+"/api/v1/ping", nil)
	req.Header.Set("Origin", "tauri://localhost")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("code = %d, want 204", resp.StatusCode)
	}
}

func TestWebSocketEcho(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/diag/ws?token=" + testToken
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: nethttp.Header{"Origin": []string{"tauri://localhost"}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageText, []byte("你好")); err != nil {
		t.Fatal(err)
	}
	_, got, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "你好" {
		t.Fatalf("echo = %q", got)
	}
}

func TestWebSocketRejectsWithoutToken(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/diag/ws"
	_, resp, err := websocket.Dial(ctx, url, nil)
	if err == nil {
		t.Fatal("没带 token 也连上了")
	}
	if resp == nil || resp.StatusCode != nethttp.StatusUnauthorized {
		t.Fatalf("resp = %v, want 401", resp)
	}
}

func upload(t *testing.T, srv *httptest.Server, filename string, content []byte) *nethttp.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	mw.Close()
	req, _ := nethttp.NewRequest("POST", srv.URL+"/api/v1/diag/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// <video> 拖动进度条靠 Range；没有 206 就只能从头播。
func TestDiagMediaRange(t *testing.T) {
	srv := newTestServer(t)
	content := []byte("0123456789abcdefghij")
	resp := upload(t, srv, "我的 视频.MP4", content)
	var out struct {
		Name string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode != nethttp.StatusOK || out.Name != "sample.mp4" {
		t.Fatalf("upload: code=%d name=%q", resp.StatusCode, out.Name)
	}

	// 不带 Origin、不带 Authorization，只靠 ?token=（<video> 的请求形态）。
	req, _ := nethttp.NewRequest("GET", srv.URL+"/api/v1/diag/media/sample.mp4?token="+testToken, nil)
	req.Header.Set("Range", "bytes=5-9")
	got, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	if got.StatusCode != nethttp.StatusPartialContent {
		t.Fatalf("code = %d, want 206", got.StatusCode)
	}
	if cr := got.Header.Get("Content-Range"); cr != "bytes 5-9/20" {
		t.Fatalf("Content-Range = %q", cr)
	}
	body, _ := io.ReadAll(got.Body)
	if string(body) != "56789" {
		t.Fatalf("body = %q", body)
	}
}

func TestDiagUploadRejectsUnknownExtension(t *testing.T) {
	srv := newTestServer(t)
	resp := upload(t, srv, "evil.sh", []byte("#!/bin/sh"))
	resp.Body.Close()
	if resp.StatusCode != nethttp.StatusBadRequest {
		t.Fatalf("code = %d, want 400", resp.StatusCode)
	}
}

// 文件名只接受固定的 sample.<ext>，任何别的名字都当不存在。
func TestDiagMediaRejectsOtherNames(t *testing.T) {
	srv := newTestServer(t)
	for _, name := range []string{"..%2F..%2Fetc%2Fpasswd", "other.mp4", "sample.sh"} {
		resp, err := nethttp.Get(srv.URL + "/api/v1/diag/media/" + name + "?token=" + testToken)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != nethttp.StatusNotFound {
			t.Fatalf("%s: code = %d, want 404", name, resp.StatusCode)
		}
	}
}

// WebSocket 握手的外来 Origin 必须被 localauth 挡下，哪怕 token 正确。
func TestWebSocketRejectsForeignOrigin(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/agent/ws?token=" + testToken
	_, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: nethttp.Header{"Origin": []string{"https://evil.example"}},
	})
	if err == nil {
		t.Fatal("外来 Origin 也连上了")
	}
	if resp == nil || resp.StatusCode != nethttp.StatusForbidden {
		t.Fatalf("resp = %v, want 403", resp)
	}
}

// 业务路由挂在 localauth 之内：没有 token 一律 401（不会因为 handler 未装配而 panic）。
func TestBusinessRoutesRequireToken(t *testing.T) {
	srv := newTestServer(t)
	for _, p := range []string{"/api/v1/me", "/api/v1/projects", "/media/u1/a.png"} {
		resp, err := nethttp.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != nethttp.StatusUnauthorized {
			t.Fatalf("%s: code = %d, want 401", p, resp.StatusCode)
		}
	}
}
