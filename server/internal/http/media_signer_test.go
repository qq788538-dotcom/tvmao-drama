package http

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
)

func TestMediaSigner_SignsThroughStorage(t *testing.T) {
	st := &stubStorage{}
	ms := NewMediaSigner(st)
	if got := ms.Sign("media://key/a.png"); got != "https://example.test/key/a.png?sig=1" {
		t.Fatalf("Sign=%q", got)
	}
	if got := ms.SignThumb("media://key/a.png", 400); !strings.HasPrefix(got, "https://example.test/key/a.png") {
		t.Fatalf("SignThumb=%q", got)
	}
}

func TestMediaSigner_DifferentKeysIndependent(t *testing.T) {
	st := &stubStorage{}
	ms := NewMediaSigner(st)
	a := ms.Sign("media://key/a.png")
	b := ms.Sign("media://key/b.png")
	if a == b {
		t.Fatalf("different keys should hash to different signed URLs, both got %q", a)
	}
	if got := st.signCalls.Load(); got != 2 {
		t.Fatalf("expected 2 SignedURL calls (one per key), got %d", got)
	}
}

func TestMediaSigner_NonMediaPassesThrough(t *testing.T) {
	st := &stubStorage{}
	ms := NewMediaSigner(st)
	external := "https://other-cdn.example/foo.png"
	if got := ms.Sign(external); got != external {
		t.Fatalf("external URL must pass unchanged, got %q", got)
	}
	if got := st.signCalls.Load(); got != 0 {
		t.Fatalf("non-media URL must not invoke SignedURL, got %d calls", got)
	}
}

func newLocalSigner(t *testing.T) *MediaSigner {
	t.Helper()
	st, err := assets.NewLocalStorage(assets.LocalConfig{
		Root: filepath.Join(t.TempDir(), "media"), BaseURL: "http://127.0.0.1:5555", Token: "mt",
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewMediaSigner(st)
}

// canonical（media://）是唯一允许落库的形态；不论 URL 来自哪次启动（端口不同）都要认回来。
func TestMediaSigner_CanonicalizeLocalURLs(t *testing.T) {
	ms := newLocalSigner(t)
	cases := map[string]string{
		"http://127.0.0.1:5555/media/u1/a.png?token=mt":   "media://u1/a.png",
		"http://127.0.0.1:40000/media/u1/a.png?token=old": "media://u1/a.png",
		"media://u1/a.png":                                   "media://u1/a.png",
		"https://cdn.example/x.png":                          "https://cdn.example/x.png",
		"http://127.0.0.1:5555/media/%E9%95%9C%E5%A4%B4.png": "media://镜头.png",
	}
	for in, want := range cases {
		if got := ms.Canonicalize(in); got != want {
			t.Errorf("Canonicalize(%q)=%q want %q", in, got, want)
		}
	}
}

func TestMediaSigner_JSONRoundTrip(t *testing.T) {
	ms := newLocalSigner(t)
	raw := json.RawMessage(`{"nodes":[{"content":"media://u1/a.png","history":["media://u1/b.png","https://cdn.example/c.png"]}]}`)
	signed := ms.SignJSON(raw)
	if strings.Contains(string(signed), "media://") || !strings.Contains(string(signed), "http://127.0.0.1:5555/media/u1/a.png?token=mt") {
		t.Fatalf("signed=%s", signed)
	}
	var want, got any
	json.Unmarshal(raw, &want)
	json.Unmarshal(ms.CanonicalizeJSON(signed), &got)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("往返不一致: %v vs %v", want, got)
	}
}

func TestMediaSigner_ObjectKey(t *testing.T) {
	ms := newLocalSigner(t)
	if got := ms.ObjectKey("http://127.0.0.1:5555/media/u1/a%20b.png?token=mt"); got != "u1/a b.png" {
		t.Fatalf("got %q", got)
	}
	if got := ms.ObjectKey("data:image/png;base64,AAAA"); got != "" {
		t.Fatalf("got %q", got)
	}
	var nilSigner *MediaSigner
	if nilSigner.ObjectKey("media://x") != "" || nilSigner.Sign("media://x") != "media://x" {
		t.Fatal("nil signer 必须安全")
	}
}
