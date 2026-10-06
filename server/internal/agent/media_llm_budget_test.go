package agent

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// 没接 Keys（P1b 未合并或合并时漏传）时，本机地址绝不能原样发给上游。
func TestLocalMediaResolver_NoKeysRejectsLoopback(t *testing.T) {
	r := &LocalMediaResolver{Objects: fakeObjects{}}
	for _, u := range []string{
		"http://127.0.0.1:5555/media/u1/a.png?token=t",
		"http://localhost:5555/media/u1/a.png",
		"http://[::1]:5555/media/u1/a.png",
		"media://u1/a.png",
	} {
		if got, err := r.ResolveForLLM(context.Background(), "image", u); err == nil {
			t.Errorf("%s 应报错，得到 %q", u, got)
		}
	}
	got, err := r.ResolveForLLM(context.Background(), "image", "https://cdn.example/a.png")
	if err != nil || got != "https://cdn.example/a.png" {
		t.Fatalf("外链应放行: %q %v", got, err)
	}
}

// 接了 Keys 但认不出的本机地址（例如 localhost、非媒体路径）同样不能外发。
func TestLocalMediaResolver_UnrecognizedLoopbackRejected(t *testing.T) {
	if got, err := newResolver().ResolveForLLM(context.Background(), "image", "http://localhost:5555/x.png"); err == nil {
		t.Fatalf("认不出的本机地址应报错，得到 %q", got)
	}
}

type countingObjects struct {
	fakeObjects
	reads atomic.Int32
}

func (c *countingObjects) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	c.reads.Add(1)
	return c.fakeObjects.GetObject(ctx, key)
}

func userImageMsg(urls ...string) *schema.Message {
	parts := []schema.ChatMessagePart{{Type: schema.ChatMessagePartTypeText, Text: "看图"}}
	for _, u := range urls {
		parts = append(parts, schema.ChatMessagePart{Type: schema.ChatMessagePartTypeImageURL, ImageURL: &schema.ChatMessageImageURL{URL: u}})
	}
	return &schema.Message{Role: schema.User, MultiContent: parts}
}

func imageParts(m *schema.Message) (dataURIs, external, notes int) {
	for _, p := range m.MultiContent {
		switch {
		case p.Type == schema.ChatMessagePartTypeImageURL && strings.HasPrefix(p.ImageURL.URL, "data:"):
			dataURIs++
		case p.Type == schema.ChatMessagePartTypeImageURL:
			external++
		case p.Type == schema.ChatMessagePartTypeText && strings.Contains(p.Text, "图片"):
			notes++
		}
	}
	return
}

// 只给最近几条带图的消息内联本机图片；更早的换成文字说明，而且不读盘。外链不受影响。
func TestResolveMediaForLLM_OnlyRecentImagesInlined(t *testing.T) {
	objs := &countingObjects{fakeObjects: fakeObjects{data: map[string][]byte{"u1/a.png": pngBytes}}}
	a := &Agent{MediaResolver: &LocalMediaResolver{Keys: fakeKeys{}, Objects: objs}}
	msgs := []*schema.Message{
		userImageMsg("media://u1/a.png", "https://cdn.example/old.png"), // 最旧
		{Role: schema.Assistant, Content: "ok"},
		userImageMsg("media://u1/a.png"),
		{Role: schema.Assistant, Content: "ok"},
		userImageMsg("media://u1/a.png"),
		{Role: schema.Assistant, Content: "ok"},
		userImageMsg("media://u1/a.png"), // 本轮
	}
	out := a.resolveMediaForLLM(context.Background(), msgs)
	if n := int(objs.reads.Load()); n != maxInlineImageMessages {
		t.Fatalf("只应读最近 %d 条消息的图片，实际读了 %d 次", maxInlineImageMessages, n)
	}
	if d, _, _ := imageParts(out[6]); d != 1 {
		t.Fatal("本轮图片必须内联")
	}
	d, ext, notes := imageParts(out[0])
	if d != 0 || ext != 1 || notes != 1 {
		t.Fatalf("最旧消息：本机图应换成文字说明、外链保留，得到 data=%d ext=%d notes=%d", d, ext, notes)
	}
	// 原 msgs 不能被改动。
	if msgs[0].MultiContent[1].ImageURL.URL != "media://u1/a.png" {
		t.Fatal("原消息被改写")
	}
}

// 单次请求内联总量封顶：超出的图片换成文字说明，优先保住最新的。
func TestResolveMediaForLLM_TotalBudget(t *testing.T) {
	r := &LocalMediaResolver{Keys: fakeKeys{}, Objects: fakeObjects{data: map[string][]byte{"u1/a.png": pngBytes, "u1/b.png": pngBytes}}}
	one, err := r.ResolveForLLM(context.Background(), "image", "media://u1/a.png")
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{MediaResolver: r, MaxInlineImageBytes: int64(len(one)) + 10}
	out := a.resolveMediaForLLM(context.Background(), []*schema.Message{userImageMsg("media://u1/a.png", "media://u1/b.png")})
	d, _, notes := imageParts(out[0])
	if d != 1 || notes != 1 {
		t.Fatalf("超预算的图片应换成文字说明，得到 data=%d notes=%d", d, notes)
	}
	if out[0].MultiContent[len(out[0].MultiContent)-1].Type != schema.ChatMessagePartTypeImageURL {
		t.Fatal("同一条消息里应优先保住靠后的（最新的）图片")
	}
}
