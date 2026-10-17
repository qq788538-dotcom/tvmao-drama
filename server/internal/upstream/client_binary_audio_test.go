package upstream

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// OpenAI 标准 /v1/audio/speech 返回裸音频字节流（非 JSON）——曾经 Call 无条件
// 按 JSON 解析,OutputURLs 为空导致任务报「上游未返回结果」。现在按响应头
// Content-Type: audio/* 识别,转成 data URL,由 persistOutputs 走既有 data: 管道落库。
func TestCall_BinaryAudioResponse(t *testing.T) {
	audioBytes := []byte{0xFF, 0xFB, 0x90, 0x64, 0x00, 0x01, 0x02, 0x03}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 带参数的 Content-Type 也要能识别并剥掉参数。
		w.Header().Set("Content-Type", "Audio/MPEG; charset=binary")
		_, _ = w.Write(audioBytes)
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL})
	res, err := c.Call(context.Background(), "sk-test", "POST", "/v1/audio/speech", []byte(`{"input":"你好"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	want := "data:audio/mpeg;base64," + base64.StdEncoding.EncodeToString(audioBytes)
	if len(res.OutputURLs) != 1 || res.OutputURLs[0] != want {
		t.Fatalf("OutputURLs = %v", res.OutputURLs)
	}
}

// JSON 响应（Content-Type: application/json）必须原样走 ExtractFields 主链路,
// 二进制分支不得截胡。
func TestCall_JSONResponseUnaffected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"audio_url":"https://cdn.example/a.mp3"}`))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL})
	res, err := c.Call(context.Background(), "", "POST", "/x", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(res.OutputURLs) != 1 || res.OutputURLs[0] != "https://cdn.example/a.mp3" {
		t.Fatalf("OutputURLs = %v", res.OutputURLs)
	}
}

// 4xx 时哪怕上游怪异地带了 audio/* 头也必须按错误处理,不能把错误响应体
// 当音频产物存起来。
func TestCall_ErrorStatusWithAudioHeaderStillFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(429)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL})
	_, err := c.Call(context.Background(), "", "POST", "/x", nil)
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected CallError, got %v", err)
	}
}

func TestBinaryAudioMime(t *testing.T) {
	cases := map[string]string{
		"audio/mpeg":                "audio/mpeg",
		"Audio/WAV; charset=binary": "audio/wav",
		"application/json":          "",
		"application/octet-stream":  "",
		"":                          "",
		"video/mp4":                 "",
	}
	for in, want := range cases {
		if got := binaryAudioMime(in); got != want {
			t.Fatalf("binaryAudioMime(%q) = %q, want %q", in, got, want)
		}
	}
}
