package upstream

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writeTemp(t *testing.T, dir, content string) string {
	t.Helper()
	f, err := os.CreateTemp(dir, "src-*.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(content)
	f.Close()
	return f.Name()
}

func TestCallMultipart_BodyFromFileWithContentLength(t *testing.T) {
	tmp := t.TempDir()
	var gotLen string
	var gotParts map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLen = r.Header.Get("Content-Length")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		gotParts = map[string]string{}
		for _, fh := range r.MultipartForm.File["image"] {
			f, _ := fh.Open()
			b, _ := io.ReadAll(f)
			f.Close()
			gotParts[fh.Filename] = string(b)
		}
		gotParts["prompt"] = r.FormValue("prompt")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"url":"https://out/1.png"}]}`))
	}))
	defer srv.Close()

	a := writeTemp(t, tmp, "AAAA")
	b := writeTemp(t, tmp, "BBBBBB")
	c := NewClient(ClientOptions{BaseURL: srv.URL, TempDir: tmp})
	res, err := c.CallMultipart(context.Background(), "sk", "POST", "/v1/images/edits",
		map[string]string{"prompt": "p"},
		[]MultipartFile{
			{FieldName: "image", Filename: "image-1.png", ContentType: "image/png", Path: a},
			{FieldName: "image", Filename: "image-2.png", ContentType: "image/png", Path: b},
		})
	if err != nil {
		t.Fatalf("CallMultipart: %v", err)
	}
	if gotLen == "" {
		t.Fatal("必须带 Content-Length,不能走 chunked")
	}
	if n, _ := strconv.Atoi(gotLen); n <= 10 {
		t.Fatalf("Content-Length 不合理: %s", gotLen)
	}
	if gotParts["image-1.png"] != "AAAA" || gotParts["image-2.png"] != "BBBBBB" || gotParts["prompt"] != "p" {
		t.Fatalf("parts = %#v", gotParts)
	}
	if len(res.OutputURLs) != 1 {
		t.Fatalf("OutputURLs = %#v", res.OutputURLs)
	}
	// multipart 临时文件必须在调用返回时已删除；源文件由调用方管,这里不动
	left, _ := filepath.Glob(filepath.Join(tmp, "mp-*"))
	if len(left) != 0 {
		t.Fatalf("multipart 临时文件残留: %v", left)
	}
}

func TestCallMultipart_MissingSourceFileFails(t *testing.T) {
	tmp := t.TempDir()
	c := NewClient(ClientOptions{BaseURL: "http://127.0.0.1:1", TempDir: tmp})
	_, err := c.CallMultipart(context.Background(), "sk", "POST", "/x", nil,
		[]MultipartFile{{FieldName: "image", Filename: "a.png", ContentType: "image/png", Path: filepath.Join(tmp, "nope.png")}})
	if err == nil {
		t.Fatal("源文件不存在应报错")
	}
	left, _ := filepath.Glob(filepath.Join(tmp, "mp-*"))
	if len(left) != 0 {
		t.Fatalf("失败也不能残留: %v", left)
	}
}
