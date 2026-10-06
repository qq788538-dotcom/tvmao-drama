package generation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

const localBase = "http://127.0.0.1:5555/media/"

// localMediaStorage 模拟 P1b 的本地存储：ExtractKey 认 media:// 和 sidecar 自己签出的本地 URL。
type localMediaStorage struct {
	AssetStorage
	files map[string][]byte
}

func (l *localMediaStorage) ExtractKey(u string) (string, bool) {
	for _, p := range []string{"media://", localBase} {
		if strings.HasPrefix(u, p) {
			return strings.TrimPrefix(u, p), true
		}
	}
	return "", false
}

func (l *localMediaStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return localBase + key, nil
}

func (l *localMediaStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := l.files[key]
	if !ok {
		return nil, errors.New("no such object")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

type planModels struct{ m *models.ModelConfig }

func (p planModels) GetByModelID(string) (*models.ModelConfig, error) { return p.m, nil }

type planKeys struct{ key string }

func (p planKeys) Resolve(int64) (string, error) { return p.key, nil }

type planSettings map[string]string

func (p planSettings) Get(k string) (string, error) { return p[k], nil }

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

func planService(m *models.ModelConfig, settings planSettings, key string) *Service {
	return &Service{
		models:      planModels{m: m},
		upstreamKey: planKeys{key: key},
		settings:    settings,
		storage:     &localMediaStorage{files: map[string][]byte{"u1/a": pngBytes}},
	}
}

func okSettings() planSettings { return planSettings{"base_domain": "https://api.example.com"} }

func TestPrepareExecutionDataURIReadsLocalFile(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, ImageInputFormat: models.ImageInputFormatDataURI,
		RequestTemplate: json.RawMessage(`{"image":"{{image}}"}`)}, okSettings(), "sk")
	plan, err := svc.prepareExecution(context.Background(), 1, "m",
		map[string]any{"images": []any{"media://u1/a"}}, "", "", nil)
	if err != nil {
		t.Fatalf("data_uri 模型应能用本地参考图: %v", err)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	if !strings.Contains(string(plan.payload), want) {
		t.Fatalf("payload 应内嵌本地文件的 data URI, got %s", plan.payload)
	}
}

func TestPrepareExecutionEditsSpoolsLocalFile(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, ImageInputFormat: models.ImageInputFormatURL,
		ImageRequestMode: models.ImageRequestModeOpenAIImageEdits,
		RequestTemplate:  json.RawMessage(`{"prompt":"{{prompt}}"}`)}, okSettings(), "sk")
	svc.tmpDir = t.TempDir()
	plan, err := svc.prepareExecution(context.Background(), 1, "m",
		map[string]any{"prompt": "p", "images": []any{"media://u1/a"}}, "", "", nil)
	if err != nil {
		t.Fatalf("multipart 模型应能用本地参考图: %v", err)
	}
	defer removePreparedFiles(plan.preparedImages)
	if len(plan.preparedImages) != 1 || plan.preparedImages[0].Path == "" {
		t.Fatalf("应落一个临时文件, got %+v", plan.preparedImages)
	}
	got, _ := os.ReadFile(plan.preparedImages[0].Path)
	if !bytes.Equal(got, pngBytes) || plan.preparedImages[0].ContentType != "image/png" {
		t.Fatalf("临时文件内容/类型不对, type=%s", plan.preparedImages[0].ContentType)
	}
}

func TestPrepareExecutionRejectsLocalMediaInURLPayload(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, ImageInputFormat: models.ImageInputFormatURL,
		RequestTemplate: json.RawMessage(`{"image":"{{image}}"}`)}, okSettings(), "sk")
	_, err := svc.prepareExecution(context.Background(), 1, "m",
		map[string]any{"images": []any{"media://u1/a"}}, "", "", nil)
	if !errors.Is(err, ErrLocalMediaUnreachable) {
		t.Fatalf("上游拿不到本机地址，应在调上游前失败, got %v", err)
	}
}

type publicSigner struct{}

func (publicSigner) SignInputMedia(_ context.Context, in map[string]any) (map[string]any, error) {
	out, err := walkInputStrings(in, func(s string) (string, error) {
		return strings.Replace(s, "media://", "https://public.example.com/", 1), nil
	})
	if err != nil {
		return nil, err
	}
	return out.(map[string]any), nil
}

func TestPrepareExecutionInjectedSignerMakesURLReachable(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, ImageInputFormat: models.ImageInputFormatURL,
		RequestTemplate: json.RawMessage(`{"image":"{{image}}"}`)}, okSettings(), "sk")
	svc.SetInputMediaSigner(publicSigner{})
	plan, err := svc.prepareExecution(context.Background(), 1, "m",
		map[string]any{"images": []any{"media://u1/a"}}, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan.payload), "https://public.example.com/u1/a") {
		t.Fatalf("S12 接入的签名器结果应进 payload, got %s", plan.payload)
	}
}

func TestPrepareExecutionMissingBaseDomain(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, RequestTemplate: json.RawMessage(`{}`)}, planSettings{}, "sk")
	_, err := svc.prepareExecution(context.Background(), 1, "m", map[string]any{}, "", "", nil)
	if !errors.Is(err, ErrBaseDomainNotConfigured) {
		t.Fatalf("got %v", err)
	}
}

func TestPrepareExecutionMissingAPIKey(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, RequestTemplate: json.RawMessage(`{}`)}, okSettings(), "")
	_, err := svc.prepareExecution(context.Background(), 1, "m", map[string]any{}, "", "", nil)
	if !errors.Is(err, ErrUpstreamKeyNotConfigured) {
		t.Fatalf("got %v", err)
	}
}
