package generation

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/models"
)

func TestPrepareImageInputsDataURI(t *testing.T) {
	fetch := func(context.Context, string) ([]byte, string, error) {
		return []byte("png-bytes"), "image/png", nil
	}
	inputs := map[string]any{
		"prompt": "hello",
		"images": []any{"https://example.com/a.png", "https://example.com/b.png"},
	}
	got, prepared, err := prepareImageInputs(context.Background(), inputs, models.ImageInputFormatDataURI, false, fetch, nil)
	if err != nil {
		t.Fatalf("prepareImageInputs: %v", err)
	}
	wantValue := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	if !reflect.DeepEqual(got["images"], []string{wantValue, wantValue}) {
		t.Fatalf("images = %#v", got["images"])
	}
	if got["image"] != wantValue {
		t.Fatalf("image = %#v", got["image"])
	}
	if inputs["image"] != nil {
		t.Fatalf("original inputs mutated: %#v", inputs)
	}
	if len(prepared) != 2 || string(prepared[0].Data) != "png-bytes" {
		t.Fatalf("prepared = %#v", prepared)
	}
}

func TestPrepareImageInputsBase64FromDataURI(t *testing.T) {
	inputData := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("jpg-bytes"))
	got, prepared, err := prepareImageInputs(context.Background(), map[string]any{"image": inputData}, models.ImageInputFormatBase64, false, nil, nil)
	if err != nil {
		t.Fatalf("prepareImageInputs: %v", err)
	}
	want := base64.StdEncoding.EncodeToString([]byte("jpg-bytes"))
	if got["image"] != want {
		t.Fatalf("image = %q, want %q", got["image"], want)
	}
	if len(prepared) != 1 || prepared[0].ContentType != "image/jpeg" {
		t.Fatalf("prepared = %#v", prepared)
	}
}

func TestPrepareImageInputsURLLoadsBytesOnlyWhenRequired(t *testing.T) {
	called := 0
	fetch := func(context.Context, string) ([]byte, string, error) {
		called++
		return []byte("img"), "image/webp", nil
	}
	inputs := map[string]any{"images": []string{"https://example.com/a.webp"}}
	got, prepared, err := prepareImageInputs(context.Background(), inputs, models.ImageInputFormatURL, false, fetch, nil)
	if err != nil {
		t.Fatalf("prepare url: %v", err)
	}
	if called != 0 {
		t.Fatalf("fetch called = %d", called)
	}
	if !reflect.DeepEqual(got["images"], []string{"https://example.com/a.webp"}) || len(prepared[0].Data) != 0 {
		t.Fatalf("unexpected url prepare: %#v %#v", got, prepared)
	}

	spool := func(context.Context, string) (spooledFile, error) {
		f, _ := os.CreateTemp(t.TempDir(), "in-*.bin")
		f.Close()
		return spooledFile{Path: f.Name(), ContentType: "image/webp"}, nil
	}
	_, prepared, err = prepareImageInputs(context.Background(), inputs, models.ImageInputFormatURL, true, fetch, spool)
	if err != nil {
		t.Fatalf("prepare url with bytes: %v", err)
	}
	if called != 0 || prepared[0].Path == "" || prepared[0].ContentType != "image/webp" {
		t.Fatalf("requireBytes 应走 spool 而不是 fetch: called=%d prepared=%#v", called, prepared)
	}
}

func TestSignInputMediaReferencesSignsMediaProtocolImages(t *testing.T) {
	storage := &fakeInputMediaStorage{
		signed: map[string]string{
			"gen/ref.png":    "https://cdn.example.com/gen/ref.png?sig=1",
			"upload/ref.png": "https://cdn.example.com/upload/ref.png?sig=2",
		},
	}
	svc := &Service{storage: storage}
	inputs := map[string]any{
		"prompt": "hello",
		"images": []string{"media://gen/ref.png", "https://outside.example.com/ref.png"},
		"nested": map[string]any{
			"image": "media://upload/ref.png",
		},
	}

	got, err := svc.signInputMediaReferences(context.Background(), inputs, false)
	if err != nil {
		t.Fatalf("signInputMediaReferences: %v", err)
	}

	if !reflect.DeepEqual(got["images"], []string{"https://cdn.example.com/gen/ref.png?sig=1", "https://outside.example.com/ref.png"}) {
		t.Fatalf("images = %#v", got["images"])
	}
	nested := got["nested"].(map[string]any)
	if nested["image"] != "https://cdn.example.com/upload/ref.png?sig=2" {
		t.Fatalf("nested image = %#v", nested["image"])
	}
	if inputs["images"].([]string)[0] != "media://gen/ref.png" {
		t.Fatalf("original inputs mutated: %#v", inputs)
	}
}

func TestScalarMultipartFieldsSkipsImageValues(t *testing.T) {
	got := scalarMultipartFields([]byte(`{"model":"gpt-image-1","prompt":"hi","n":2,"transparent":true,"images":["x"],"extra_body":{"x":1}}`))
	want := map[string]string{
		"model":       "gpt-image-1",
		"prompt":      "hi",
		"n":           "2",
		"transparent": "true",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %#v, want %#v", got, want)
	}
}

func TestSanitizeTaskPayloadRedactsImageBodies(t *testing.T) {
	hugeB64 := base64.StdEncoding.EncodeToString(make([]byte, 4097))
	raw := []byte(`{"prompt":"hi","image":"data:image/png;base64,AAAA","nested":{"b64":"` + hugeB64 + `"},"url":"https://example.com/a.png"}`)
	got := string(sanitizeTaskPayload(raw))
	if got == string(raw) {
		t.Fatalf("payload was not sanitized")
	}
	if !strings.Contains(got, "[redacted image data_uri]") || !strings.Contains(got, "[redacted image base64]") {
		t.Fatalf("missing redactions: %s", got)
	}
	if !strings.Contains(got, "https://example.com/a.png") {
		t.Fatalf("url should be preserved: %s", got)
	}
}

type fakeInputMediaStorage struct {
	signed map[string]string
}

func (s *fakeInputMediaStorage) PutObject(context.Context, assets.PutObjectInput) (assets.StoredObject, error) {
	return assets.StoredObject{}, errors.New("not used")
}

func (s *fakeInputMediaStorage) DeleteObject(context.Context, string) error {
	return errors.New("not used")
}

func (s *fakeInputMediaStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	if u := s.signed[key]; u != "" {
		return u, nil
	}
	return "", errors.New("missing signed url")
}

func (s *fakeInputMediaStorage) SignedThumbURL(context.Context, string, time.Duration, int) (string, error) {
	return "", errors.New("not used")
}

func (s *fakeInputMediaStorage) ExtractKey(urlOrKey string) (string, bool) {
	const pfx = "media://"
	if strings.HasPrefix(urlOrKey, pfx) {
		return strings.TrimPrefix(urlOrKey, pfx), true
	}
	return "", false
}

func (s *fakeInputMediaStorage) GetObject(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not used")
}

func (s *fakeInputMediaStorage) StaticKey(userID int64, filename string) string {
	return s.NewKey(userID, filename)
}
func (s *fakeInputMediaStorage) NewKey(int64, string) string {
	return ""
}

func (s *fakeInputMediaStorage) CopyObject(context.Context, string, string, string) (assets.StoredObject, error) {
	return assets.StoredObject{}, errors.New("not used")
}
