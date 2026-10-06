package generation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
)

var errRealPersonNotReady = errors.New("真人图尚未通过合规校验")

type fakeRPResolver struct {
	resolve func(userID int64, objectKey string) (string, error)
}

func (f fakeRPResolver) Resolve(userID int64, objectKey string) (string, error) {
	return f.resolve(userID, objectKey)
}

type fakeKeyStorage struct{}

func (fakeKeyStorage) PutObject(context.Context, assets.PutObjectInput) (assets.StoredObject, error) {
	panic("not used")
}

func (fakeKeyStorage) DeleteObject(context.Context, string) error {
	panic("not used")
}

func (fakeKeyStorage) SignedURL(context.Context, string, time.Duration) (string, error) {
	panic("not used")
}

func (fakeKeyStorage) NewKey(int64, string) string {
	panic("not used")
}
func (fakeKeyStorage) StaticKey(userID int64, filename string) string { return filename }

func (fakeKeyStorage) ExtractKey(ref string) (string, bool) {
	const prefix = "media://"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	return strings.TrimPrefix(ref, prefix), true
}

func (fakeKeyStorage) GetObject(context.Context, string) (io.ReadCloser, error) {
	panic("not used")
}

func TestReplaceRealPersonHit(t *testing.T) {
	svc := &Service{
		storage: fakeKeyStorage{},
		realPerson: fakeRPResolver{resolve: func(userID int64, objectKey string) (string, error) {
			if userID != 42 {
				t.Fatalf("userID = %d, want 42", userID)
			}
			if objectKey != "library/7/a.png" {
				t.Fatalf("objectKey = %q, want library/7/a.png", objectKey)
			}
			return "asset://A1", nil
		}},
	}

	got, err := svc.replaceRealPersonImageRefs(42, map[string]any{
		"images": []string{"media://library/7/a.png", "https://外部图"},
	})
	if err != nil {
		t.Fatalf("replaceRealPersonImageRefs() error = %v", err)
	}
	want := []string{"asset://A1", "https://外部图"}
	images, ok := got["images"].([]string)
	if !ok || len(images) != len(want) {
		t.Fatalf("images = %#v, want %#v", got["images"], want)
	}
	for i := range want {
		if images[i] != want[i] {
			t.Fatalf("images[%d] = %q, want %q", i, images[i], want[i])
		}
	}
}

func TestReplaceRealPersonNotReady(t *testing.T) {
	svc := &Service{
		storage: fakeKeyStorage{},
		realPerson: fakeRPResolver{resolve: func(int64, string) (string, error) {
			return "", fmt.Errorf("resolve real person: %w", errRealPersonNotReady)
		}},
	}

	_, err := svc.replaceRealPersonImageRefs(42, map[string]any{
		"images": []string{"media://library/7/a.png"},
	})
	if !errors.Is(err, errRealPersonNotReady) {
		t.Fatalf("error = %v, want ErrRealPersonNotReady", err)
	}
}

func TestReplaceRealPersonMiss(t *testing.T) {
	svc := &Service{
		storage: fakeKeyStorage{},
		realPerson: fakeRPResolver{resolve: func(int64, string) (string, error) {
			return "", nil
		}},
	}
	inputs := map[string]any{
		"images": []string{"media://library/7/a.png"},
		"prompt": "keep me",
	}

	got, err := svc.replaceRealPersonImageRefs(42, inputs)
	if err != nil {
		t.Fatalf("replaceRealPersonImageRefs() error = %v", err)
	}
	images, ok := got["images"].([]string)
	if !ok || len(images) != 1 || images[0] != "media://library/7/a.png" {
		t.Fatalf("images = %#v, want original reference", got["images"])
	}
	if got["prompt"] != "keep me" {
		t.Fatalf("prompt = %#v, want keep me", got["prompt"])
	}
}

func TestReplaceSingleImageField(t *testing.T) {
	svc := &Service{
		storage: fakeKeyStorage{},
		realPerson: fakeRPResolver{resolve: func(_ int64, objectKey string) (string, error) {
			if objectKey != "library/7/a.png" {
				t.Fatalf("objectKey = %q, want library/7/a.png", objectKey)
			}
			return "asset://A1", nil
		}},
	}

	got, err := svc.replaceRealPersonImageRefs(42, map[string]any{
		"image": "media://library/7/a.png",
	})
	if err != nil {
		t.Fatalf("replaceRealPersonImageRefs() error = %v", err)
	}
	if got["image"] != "asset://A1" {
		t.Fatalf("image = %#v, want asset://A1", got["image"])
	}
}

// 视频/音频参考同样吃 asset:// 替换——画布连线喂进来的字段是 videos/audios
// （见 web 的 buildVideoGenerationInputs），只扫 images 会让这两类校验白做。
func TestReplaceRealPersonCoversVideoAndAudio(t *testing.T) {
	svc := &Service{
		storage: fakeKeyStorage{},
		realPerson: fakeRPResolver{resolve: func(_ int64, objectKey string) (string, error) {
			return "asset://" + objectKey, nil
		}},
	}

	got, err := svc.replaceRealPersonImageRefs(42, map[string]any{
		"videos": []string{"media://canvas/v.mp4"},
		"audios": []string{"media://canvas/a.mp3"},
	})
	if err != nil {
		t.Fatalf("replaceRealPersonImageRefs() error = %v", err)
	}
	if v := got["videos"].([]string); v[0] != "asset://canvas/v.mp4" {
		t.Fatalf("videos[0] = %q", v[0])
	}
	if a := got["audios"].([]string); a[0] != "asset://canvas/a.mp3" {
		t.Fatalf("audios[0] = %q", a[0])
	}
}

// 没做过校验的素材必须原样透传。这条是「扩到视频/音频不影响既有生成路径」的
// 保证：项目管理的角色配音走的是同一个 audios 通道，但它从不提交校验。
func TestReplaceRealPersonUnverifiedMediaUntouched(t *testing.T) {
	svc := &Service{
		storage:    fakeKeyStorage{},
		realPerson: fakeRPResolver{resolve: func(int64, string) (string, error) { return "", nil }},
	}

	got, err := svc.replaceRealPersonImageRefs(42, map[string]any{
		"audios": []string{"media://drama/voice.mp3"},
	})
	if err != nil {
		t.Fatalf("replaceRealPersonImageRefs() error = %v", err)
	}
	if a := got["audios"].([]string); a[0] != "media://drama/voice.mp3" {
		t.Fatalf("未校验的音频被改写了: %q", a[0])
	}
}
