package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/james/huabu_studio/apps/api/internal/billing"
)

// fakeChatModel 返回一个预设的响应，并记录它看到的消息。
type fakeChatModel struct {
	reply string
	got   []*schema.Message
}

func (m *fakeChatModel) Generate(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.got = msgs
	return &schema.Message{Role: schema.Assistant, Content: m.reply}, nil
}

func (m *fakeChatModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("not implemented")
}

func (m *fakeChatModel) BindTools(_ []*schema.ToolInfo) error { return nil }

func TestAnalyzeImageTool_RunsAndReturnsContent(t *testing.T) {
	fake := &fakeChatModel{reply: "a shiba inu in snow"}
	builder := func(_ context.Context, _ int64, _ PlannerDeps) (model.ChatModel, billing.ModelBilling, error) {
		return fake, billing.ModelBilling{}, nil
	}
	tool := NewAnalyzeImageTool(PlannerDeps{}, builder)
	ctx := WithUserID(context.Background(), 42)

	out, err := tool.InvokableRun(ctx, `{"url":"http://x/dog.png","question":"what is in this image?"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if out != "a shiba inu in snow" {
		t.Fatalf("content: %q", out)
	}
	if len(fake.got) != 1 {
		t.Fatalf("want 1 message, got %d", len(fake.got))
	}
	parts := fake.got[0].MultiContent
	if len(parts) != 2 {
		t.Fatalf("want 2 parts, got %d", len(parts))
	}
	if parts[0].Type != schema.ChatMessagePartTypeText || !strings.Contains(parts[0].Text, "what is in this image") {
		t.Fatalf("text part: %+v", parts[0])
	}
	if parts[1].Type != schema.ChatMessagePartTypeImageURL || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "http://x/dog.png" {
		t.Fatalf("image part: %+v", parts[1])
	}
}

func TestAnalyzeVideoTool_RunsAndReturnsContent(t *testing.T) {
	fake := &fakeChatModel{reply: "a cat playing the piano"}
	builder := func(_ context.Context, _ int64, _ PlannerDeps) (model.ChatModel, billing.ModelBilling, error) {
		return fake, billing.ModelBilling{}, nil
	}
	tool := NewAnalyzeVideoTool(PlannerDeps{}, builder)
	ctx := WithUserID(context.Background(), 1)

	out, err := tool.InvokableRun(ctx, `{"url":"http://x/clip.mp4","question":"summarise the action"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if out != "a cat playing the piano" {
		t.Fatalf("content: %q", out)
	}
	// 视频必须以 video_url 部件发出。曾经装在 image_url 里，上游按图片解码 mp4
	// 直接失败——所以这里钉死部件类型，不只钉「跑通」。
	parts := fake.got[0].MultiContent
	if len(parts) != 2 {
		t.Fatalf("want 2 parts, got %d", len(parts))
	}
	if parts[1].Type != schema.ChatMessagePartTypeVideoURL || parts[1].VideoURL == nil || parts[1].VideoURL.URL != "http://x/clip.mp4" {
		t.Fatalf("video part: %+v", parts[1])
	}
}

func TestAnalyzeAudioTool_RunsAndSendsAudioPart(t *testing.T) {
	fake := &fakeChatModel{reply: "upbeat jazz with piano"}
	builder := func(_ context.Context, _ int64, _ PlannerDeps) (model.ChatModel, billing.ModelBilling, error) {
		return fake, billing.ModelBilling{}, nil
	}
	tool := NewAnalyzeAudioTool(PlannerDeps{}, builder)
	ctx := WithUserID(context.Background(), 1)

	out, err := tool.InvokableRun(ctx, `{"url":"http://x/song.wav?sig=abc","question":"describe the music"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if out != "upbeat jazz with piano" {
		t.Fatalf("content: %q", out)
	}
	parts := fake.got[0].MultiContent
	if len(parts) != 2 {
		t.Fatalf("want 2 parts, got %d", len(parts))
	}
	if parts[1].Type != schema.ChatMessagePartTypeAudioURL || parts[1].AudioURL == nil || parts[1].AudioURL.URL != "http://x/song.wav?sig=abc" {
		t.Fatalf("audio part: %+v", parts[1])
	}
	// MIMEType 从扩展名推断（签名 URL 的 query 不参与）；openai 适配层把它转成
	// input_audio.format，OpenAI 兼容协议的取值是 "wav"/"mp3" 这类短格式。
	if parts[1].AudioURL.MIMEType != "wav" {
		t.Fatalf("audio format: %q", parts[1].AudioURL.MIMEType)
	}
}

func TestAudioFormatFromURL(t *testing.T) {
	cases := map[string]string{
		"http://x/a.mp3":       "mp3",
		"http://x/a.WAV?sig=1": "wav",
		"http://x/a.m4a":       "m4a",
		"http://x/noext":       "mp3",
		"::not-a-url::":        "mp3",
	}
	for in, want := range cases {
		if got := audioFormatFromURL(in); got != want {
			t.Errorf("audioFormatFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnalyzeMediaTool_BuilderError(t *testing.T) {
	builder := func(_ context.Context, _ int64, _ PlannerDeps) (model.ChatModel, billing.ModelBilling, error) {
		return nil, billing.ModelBilling{}, errors.New("backbone unavailable")
	}
	tool := NewAnalyzeImageTool(PlannerDeps{}, builder)
	ctx := WithUserID(context.Background(), 1)
	if _, err := tool.InvokableRun(ctx, `{"url":"http://x.png","question":"q"}`); err == nil {
		t.Fatal("expected error from builder")
	}
}

func TestAnalyzeMediaTool_MissingArgs(t *testing.T) {
	tool := NewAnalyzeImageTool(PlannerDeps{}, func(_ context.Context, _ int64, _ PlannerDeps) (model.ChatModel, billing.ModelBilling, error) {
		return nil, billing.ModelBilling{}, nil
	})
	ctx := WithUserID(context.Background(), 1)
	if _, err := tool.InvokableRun(ctx, `{"url":"","question":"q"}`); err == nil {
		t.Fatal("expected url-required error")
	}
	if _, err := tool.InvokableRun(ctx, `{"url":"x","question":""}`); err == nil {
		t.Fatal("expected question-required error")
	}
}

func TestAnalyzeMediaTool_MissingUserID(t *testing.T) {
	tool := NewAnalyzeImageTool(PlannerDeps{}, func(_ context.Context, _ int64, _ PlannerDeps) (model.ChatModel, billing.ModelBilling, error) {
		return nil, billing.ModelBilling{}, nil
	})
	if _, err := tool.InvokableRun(context.Background(), `{"url":"x","question":"q"}`); err == nil {
		t.Fatal("expected missing-user-id error")
	}
}
