package generation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"sort"
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

type fakePublisher struct {
	configured bool
	published  []string
}

func (f *fakePublisher) Configured() bool { return f.configured }
func (f *fakePublisher) Publish(_ context.Context, key string) (string, error) {
	f.published = append(f.published, key)
	return "https://bucket.example.com/" + key + "?sig", nil
}

func TestPrepareExecutionPublishesVideo(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, ImageInputFormat: models.ImageInputFormatURL,
		RequestTemplate: json.RawMessage(`{"video":"{{video}}"}`)}, okSettings(), "sk")
	pub := &fakePublisher{configured: true}
	svc.SetMediaPublisher(pub)
	plan, err := svc.prepareExecution(context.Background(), 1, "m",
		map[string]any{"video": "media://u1/v.mp4"}, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan.payload), "https://bucket.example.com/u1/v.mp4?sig") {
		t.Fatalf("视频应换成对象存储地址, got %s", plan.payload)
	}
}

func TestSignInputMediaSkipsImagesForInlineFormats(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format string
		mode   string
		skip   bool
	}{
		{"data_uri", models.ImageInputFormatDataURI, models.ImageRequestModeJSON, true},
		{"edits", models.ImageInputFormatURL, models.ImageRequestModeOpenAIImageEdits, true},
		{"url", models.ImageInputFormatURL, models.ImageRequestModeJSON, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := skipImagesFor(&models.ModelConfig{ImageInputFormat: tc.format, ImageRequestMode: tc.mode}); got != tc.skip {
				t.Fatalf("skipImagesFor=%v want %v", got, tc.skip)
			}
			svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true}, okSettings(), "sk")
			pub := &fakePublisher{configured: true}
			svc.SetMediaPublisher(pub)
			out, err := svc.signInputMediaReferences(context.Background(),
				map[string]any{"images": []any{"media://u1/a.png"}, "videos": []any{"media://u1/v.mp4"}}, tc.skip)
			if err != nil {
				t.Fatal(err)
			}
			wantPub := []string{"u1/v.mp4"}
			if !tc.skip {
				wantPub = []string{"u1/a.png", "u1/v.mp4"}
			}
			sort.Strings(pub.published)
			if !reflect.DeepEqual(pub.published, wantPub) {
				t.Fatalf("published=%v want %v", pub.published, wantPub)
			}
			if tc.skip && out["images"].([]any)[0] != "media://u1/a.png" {
				t.Fatalf("跳过的图片应原样保留, got %v", out["images"])
			}
		})
	}
}

func TestPrepareExecutionUnconfiguredStillFailsWithGuide(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, ImageInputFormat: models.ImageInputFormatURL,
		RequestTemplate: json.RawMessage(`{"video":"{{video}}"}`)}, okSettings(), "sk")
	svc.SetMediaPublisher(&fakePublisher{configured: false})
	_, err := svc.prepareExecution(context.Background(), 1, "m",
		map[string]any{"video": "media://u1/v.mp4"}, "", "", nil)
	if !errors.Is(err, ErrLocalMediaUnreachable) || !strings.Contains(err.Error(), "设置 → 配置 → 对象存储") {
		t.Fatalf("err=%v", err)
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
