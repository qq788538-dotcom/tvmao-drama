package generation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
)

// captureStorage 记录 PutObject 收到的字节与 size，可注入失败。
type captureStorage struct {
	fakeInputMediaStorage
	got   map[string][]byte
	sizes map[string]int64
	fail  bool
}

func (c *captureStorage) StaticKey(userID int64, name string) string {
	return "u/" + name
}

func (c *captureStorage) PutObject(_ context.Context, in assets.PutObjectInput) (assets.StoredObject, error) {
	if c.fail {
		return assets.StoredObject{}, errors.New("tos down")
	}
	r, size := in.Content()
	b, err := io.ReadAll(r)
	if err != nil {
		return assets.StoredObject{}, err
	}
	if c.got == nil {
		c.got, c.sizes = map[string][]byte{}, map[string]int64{}
	}
	c.got[in.Key] = b
	c.sizes[in.Key] = size
	return assets.StoredObject{URL: assets.MediaProtocol + in.Key, Size: size}, nil
}

func writePNG(t *testing.T) (string, []byte) {
	t.Helper()
	body := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 600)...)
	p := filepath.Join(t.TempDir(), "out-1.png")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, body
}

type failRT struct{}

func (failRT) RoundTrip(*http.Request) (*http.Response, error) { return nil, errors.New("dial fail") }

func httpClientAlwaysFail() *http.Client { return &http.Client{Transport: failRT{}} }

func TestPersistOutputs_FileURLStreamsExactBytes(t *testing.T) {
	p, want := writePNG(t)
	st := &captureStorage{}
	s := &Service{storage: st, tmpDir: filepath.Dir(p)}
	urls, ids, mimes, _, err := s.persistOutputs(context.Background(), 64, 33888, nil, []string{"file://" + p}, 0)
	if err != nil {
		t.Fatalf("persistOutputs: %v", err)
	}
	if len(urls) != 1 || urls[0] != assets.MediaProtocol+"u/gen/task-33888-0" {
		t.Fatalf("urls = %#v", urls)
	}
	if string(st.got["u/gen/task-33888-0"]) != string(want) || st.sizes["u/gen/task-33888-0"] != int64(len(want)) {
		t.Fatal("上传字节/长度必须与临时文件逐字相同")
	}
	if mimes[0] != "image/png" {
		t.Fatalf("mime 应从文件头探测: %q", mimes[0])
	}
	if len(ids) != 1 {
		t.Fatalf("ids = %#v", ids)
	}
}

func TestPersistOutputs_FileURLUploadFailureIsError(t *testing.T) {
	p, _ := writePNG(t)
	s := &Service{storage: &captureStorage{fail: true}, tmpDir: filepath.Dir(p)}
	_, _, _, _, err := s.persistOutputs(context.Background(), 64, 1, nil, []string{"file://" + p}, 0)
	if !errors.Is(err, errLocalPersist) {
		t.Fatalf("file:// 上传失败必须报 errLocalPersist, got %v", err)
	}
}

func TestPersistOutputs_HTTPFailureStillKeepsUpstreamURL(t *testing.T) {
	zeroPersistRetryDelays(t)
	// 现状语义不变：http(s) 拉不到就保留上游 URL,不报错。
	s := &Service{storage: &captureStorage{}, httpClient: httpClientAlwaysFail()}
	urls, ids, _, _, err := s.persistOutputs(context.Background(), 64, 1, nil, []string{"https://gone.example/1.png"}, 0)
	if err != nil {
		t.Fatalf("http 失败不应返回 error: %v", err)
	}
	if urls[0] != "https://gone.example/1.png" || ids[0] != 0 {
		t.Fatalf("应保留上游 URL: %#v %#v", urls, ids)
	}
}
