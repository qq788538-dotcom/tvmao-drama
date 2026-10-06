package upstream

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

const (
	secretKey    = "sk-SECRET-KEY-123"
	secretPreset = "PRESET-BODY-CONFIDENTIAL"
)

// I2：请求体、成功响应体都不进日志；失败响应体只留折叠 data URI 后的前 512 字节。
func TestCallLogsNoBodiesOrSecrets(t *testing.T) {
	big := strings.Repeat("A", 20000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad","img":"data:image/png;base64,` + big + `","tail":"` + strings.Repeat("z", 2000) + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"url":"https://cdn/x.png","revised_prompt":"` + secretPreset + `"}]}`))
	}))
	defer srv.Close()
	buf := captureLog(t)
	c := NewClient(ClientOptions{BaseURL: srv.URL, TempDir: t.TempDir()})
	payload := []byte(`{"prompt":"` + secretPreset + ` user words","image":"data:image/png;base64,` + big + `"}`)
	if _, err := c.Call(context.Background(), secretKey, "POST", "/ok", payload); err != nil {
		t.Fatal(err)
	}
	_, err := c.Call(context.Background(), secretKey, "POST", "/fail", payload)
	if err == nil {
		t.Fatal("want error")
	}
	out := buf.String()
	for _, banned := range []string{secretKey, secretPreset, "user words", strings.Repeat("A", 64), strings.Repeat("z", 600)} {
		if strings.Contains(out, banned) {
			t.Fatalf("日志里出现了不该出现的内容 %.40q:\n%s", banned, out)
		}
	}
	if !strings.Contains(out, "status=200") || !strings.Contains(out, "status=400") || !strings.Contains(out, "bytes=") {
		t.Fatalf("日志应记状态码和字节数:\n%s", out)
	}
	if !strings.Contains(out, `"error":"bad"`) || !strings.Contains(out, "data:image/png;base64,…") {
		t.Fatalf("失败响应应留折叠后的前缀:\n%s", out)
	}
	// 错误文本（会进任务 error 和日志）也要有界。
	if len(err.Error()) > 1024 || strings.Contains(err.Error(), strings.Repeat("A", 64)) {
		t.Fatalf("CallError 文本应折叠截断, len=%d", len(err.Error()))
	}
}

func TestCallMultipartLogsNoBodies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"url":"https://cdn/x.png","revised_prompt":"` + secretPreset + `"}]}`))
	}))
	defer srv.Close()
	buf := captureLog(t)
	tmp := t.TempDir()
	c := NewClient(ClientOptions{BaseURL: srv.URL, TempDir: tmp})
	_, err := c.CallMultipart(context.Background(), secretKey, "POST", "/v1/images/edits",
		map[string]string{"prompt": secretPreset},
		[]MultipartFile{{FieldName: "image", Filename: "a.png", ContentType: "image/png", Path: writeTemp(t, tmp, "aaa")}})
	if err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Contains(out, secretPreset) || strings.Contains(out, secretKey) {
		t.Fatalf("multipart 日志泄漏:\n%s", out)
	}
}

func TestFoldDataURIs(t *testing.T) {
	got := foldDataURIs(`a "data:image/png;base64,QUJD" b data:audio/mpeg;base64,xyz== c`)
	want := `a "data:image/png;base64,…" b data:audio/mpeg;base64,… c`
	if got != want {
		t.Fatalf("got %q", got)
	}
}
