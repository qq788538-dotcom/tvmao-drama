package generation

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

func TestPrepareImageInputs_EditsModeSpoolsToFile(t *testing.T) {
	dir := t.TempDir()
	spool := func(_ context.Context, ref string) (spooledFile, error) {
		f, err := os.CreateTemp(dir, "in-*.bin")
		if err != nil {
			return spooledFile{}, err
		}
		defer f.Close()
		_, _ = f.WriteString("png-" + ref)
		return spooledFile{Path: f.Name(), Size: int64(4 + len(ref)), ContentType: "image/png"}, nil
	}
	inputs := map[string]any{"images": []any{"https://x/a.png", "https://x/b.png"}}
	_, prepared, err := prepareImageInputs(context.Background(), inputs, models.ImageInputFormatURL, true, nil, spool)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(prepared) != 2 || prepared[0].Path == "" || prepared[0].Size != int64(len("png-https://x/a.png")) {
		t.Fatalf("prepared = %#v", prepared)
	}
	if len(prepared[0].Data) != 0 {
		t.Fatal("edits 路径不应再把字节留在内存")
	}
	removePreparedFiles(prepared)
	if _, err := os.Stat(prepared[0].Path); !os.IsNotExist(err) {
		t.Fatal("removePreparedFiles 应删掉临时文件")
	}
}

func TestPrepareImageInputs_SpoolErrorCleansEarlierFiles(t *testing.T) {
	dir := t.TempDir()
	var first string
	spool := func(_ context.Context, ref string) (spooledFile, error) {
		if strings.HasSuffix(ref, "b.png") {
			return spooledFile{}, errors.New("boom")
		}
		f, _ := os.CreateTemp(dir, "in-*.bin")
		f.Close()
		first = f.Name()
		return spooledFile{Path: f.Name(), ContentType: "image/png"}, nil
	}
	inputs := map[string]any{"images": []any{"https://x/a.png", "https://x/b.png"}}
	if _, _, err := prepareImageInputs(context.Background(), inputs, models.ImageInputFormatURL, true, nil, spool); err == nil {
		t.Fatal("第二张失败应报错")
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("失败时第一张的临时文件应被清掉")
	}
}

func TestPrepareImageInputs_EditsModeWithoutSpoolErrors(t *testing.T) {
	inputs := map[string]any{"images": []any{"https://x/a.png"}}
	if _, _, err := prepareImageInputs(context.Background(), inputs, models.ImageInputFormatURL, true, nil, nil); err == nil {
		t.Fatal("requireBytes 且无 spool 必须报错,不能静默退回内存路径")
	}
}

func TestSpoolInputImage_HTTPStreamsToFileWithLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg-bytes"))
	}))
	defer srv.Close()
	s := &Service{httpClient: srv.Client()}
	s.SetTempDir(t.TempDir())
	got, err := s.spoolInputImage(context.Background(), srv.URL+"/a.jpg")
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	b, _ := os.ReadFile(got.Path)
	if string(b) != "jpeg-bytes" || got.Size != 10 || got.ContentType != "image/jpeg" {
		t.Fatalf("got = %#v body=%q", got, b)
	}
	if !strings.HasPrefix(got.Path, s.tempDir()) {
		t.Fatalf("应落在临时目录: %s", got.Path)
	}
}

func TestSpoolInputImage_OverLimitRejectsAndRemoves(t *testing.T) {
	big := make([]byte, maxInputImageBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(big)
	}))
	defer srv.Close()
	s := &Service{httpClient: srv.Client()}
	dir := t.TempDir()
	s.SetTempDir(dir)
	if _, err := s.spoolInputImage(context.Background(), srv.URL+"/big.png"); err == nil {
		t.Fatal("超限应报错")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("超限的临时文件应被删除, 残留 %d", len(entries))
	}
}

func TestSpoolInputImage_DataURL(t *testing.T) {
	s := &Service{}
	s.SetTempDir(t.TempDir())
	ref := "data:image/webp;base64," + base64.StdEncoding.EncodeToString([]byte("webp!"))
	got, err := s.spoolInputImage(context.Background(), ref)
	if err != nil {
		t.Fatalf("spool data url: %v", err)
	}
	b, _ := os.ReadFile(got.Path)
	if string(b) != "webp!" || got.ContentType != "image/webp" || got.Size != 5 {
		t.Fatalf("got=%#v body=%q", got, b)
	}
}
