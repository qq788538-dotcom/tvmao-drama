package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/billing"
)

// 最小 PNG 头，足够让 http.DetectContentType 认成 image/png。
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 32)...)

const localURL = "http://127.0.0.1:5555/api/v1/media/u1/a.png?token=t"

type fakeKeys struct{}

func (fakeKeys) ExtractKey(s string) (string, bool) {
	if strings.HasPrefix(s, "http://127.0.0.1:5555/api/v1/media/") {
		return strings.TrimSuffix(strings.TrimPrefix(s, "http://127.0.0.1:5555/api/v1/media/"), "?token=t"), true
	}
	if strings.HasPrefix(s, "media://") {
		return strings.TrimPrefix(s, "media://"), true
	}
	return "", false
}

type fakeObjects struct{ data map[string][]byte }

func (f fakeObjects) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := f.data[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func newResolver() *LocalMediaResolver {
	return &LocalMediaResolver{Keys: fakeKeys{}, Objects: fakeObjects{data: map[string][]byte{"u1/a.png": pngBytes, "u1/v.mp4": []byte("....ftypmp42")}}}
}

func TestLocalMediaResolver_ImageBecomesDataURI(t *testing.T) {
	got, err := newResolver().ResolveForLLM(context.Background(), "image", localURL)
	if err != nil || !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestLocalMediaResolver_ExternalURLPassesThrough(t *testing.T) {
	got, err := newResolver().ResolveForLLM(context.Background(), "video", "https://cdn.example/v.mp4")
	if err != nil || got != "https://cdn.example/v.mp4" {
		t.Fatalf("外链应原样放行: %q %v", got, err)
	}
}

func TestLocalMediaResolver_LocalVideoNeedsPublicURL(t *testing.T) {
	_, err := newResolver().ResolveForLLM(context.Background(), "video", "media://u1/v.mp4")
	if !errors.Is(err, ErrMediaNeedsPublicURL) {
		t.Fatalf("本机视频应返回 ErrMediaNeedsPublicURL，得到 %v", err)
	}
}

func TestLocalMediaResolver_TooLargeImage(t *testing.T) {
	r := newResolver()
	r.MaxImageBytes = 10
	if _, err := r.ResolveForLLM(context.Background(), "image", localURL); err == nil {
		t.Fatal("超限图片应报错")
	}
}

// @ 本机图片：发给 planner 的是 data URI，落库的 image block 仍是原地址。
func TestRun_LocalImageMentionSentAsDataURI(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{{{Role: schema.Assistant, Content: "OK"}}}}
	repo := newInMemRepo()
	_ = repo.CreateThread(Thread{ID: "thr_1", ProjectID: 1, UserID: 1})
	a := &Agent{Planner: llm, Tools: newStubRegistryWith(), Repo: repo, Sink: &stubFrameSink{}, MediaResolver: newResolver()}
	mentions := []Mention{{ArtifactID: "art_1", URL: localURL, Name: "图", Kind: "image"}}
	if err := a.Run(context.Background(), "thr_1", "act_1", "看图", mentions, nil); err != nil {
		t.Fatal(err)
	}
	last := llm.lastInputs[0][len(llm.lastInputs[0])-1]
	found := false
	for _, p := range last.MultiContent {
		if p.Type == schema.ChatMessagePartTypeImageURL && strings.HasPrefix(p.ImageURL.URL, "data:image/png;base64,") {
			found = true
		}
	}
	if !found {
		t.Fatalf("planner 没收到 data URI: %+v", last.MultiContent)
	}
	saved, _ := repo.ListMessages("thr_1")
	if strings.Contains(string(saved[0].ContentBlocks), "data:image") {
		t.Fatal("data URI 不得落库")
	}
}

func TestAnalyzeVideoTool_LocalVideoReturnsS12Error(t *testing.T) {
	fake := &fakeChatModel{reply: "x"}
	builder := func(context.Context, int64, PlannerDeps) (model.ChatModel, billing.ModelBilling, error) {
		return fake, billing.ModelBilling{}, nil
	}
	tool := NewAnalyzeVideoTool(PlannerDeps{Media: newResolver()}, builder)
	_, err := tool.InvokableRun(WithUserID(context.Background(), 1), `{"url":"media://u1/v.mp4","question":"讲了什么"}`)
	if err == nil || !strings.Contains(err.Error(), "对象存储") {
		t.Fatalf("应返回 S12 中文错误，得到 %v", err)
	}
	if fake.got != nil {
		t.Fatal("不应调用上游模型")
	}
}

func TestAnalyzeImageTool_LocalImageSentAsDataURI(t *testing.T) {
	fake := &fakeChatModel{reply: "一张图"}
	builder := func(context.Context, int64, PlannerDeps) (model.ChatModel, billing.ModelBilling, error) {
		return fake, billing.ModelBilling{}, nil
	}
	tool := NewAnalyzeImageTool(PlannerDeps{Media: newResolver()}, builder)
	if _, err := tool.InvokableRun(WithUserID(context.Background(), 1), `{"url":"`+localURL+`","question":"是什么"}`); err != nil {
		t.Fatal(err)
	}
	if u := fake.got[0].MultiContent[1].ImageURL.URL; !strings.HasPrefix(u, "data:image/png;base64,") {
		t.Fatalf("子模型收到的不是 data URI: %q", u)
	}
}

type fakePub struct{ configured bool }

func (f fakePub) Configured() bool { return f.configured }
func (f fakePub) Publish(_ context.Context, key string) (string, error) {
	return "https://bucket.example.com/" + key, nil
}

func TestResolverPublishesVideoAndAudio(t *testing.T) {
	r := newResolver()
	r.Publisher = fakePub{configured: true}
	for _, kind := range []string{"video", "audio"} {
		got, err := r.ResolveForLLM(context.Background(), kind, "media://u1/v.mp4")
		if err != nil || got != "https://bucket.example.com/u1/v.mp4" {
			t.Fatalf("%s: got %q err=%v", kind, got, err)
		}
	}
}

func TestResolverVideoUnconfiguredGuides(t *testing.T) {
	r := newResolver()
	r.Publisher = fakePub{configured: false}
	_, err := r.ResolveForLLM(context.Background(), "video", "media://u1/v.mp4")
	if !errors.Is(err, ErrMediaNeedsPublicURL) || !strings.Contains(err.Error(), "设置 → 对象存储") {
		t.Fatalf("err=%v", err)
	}
}
