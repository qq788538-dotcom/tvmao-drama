package agent

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func capturePlannerLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

// 上游报错时日志只能有状态码和截断后的响应：请求体里有用户对话和图片 data URI，
// 不能落进日志（会撑爆磁盘、随诊断包外泄）。
func TestPlannerLogTransport_ErrorDoesNotLogRequestBody(t *testing.T) {
	buf := capturePlannerLog(t)
	secret := "用户的私密对话内容"
	img := "data:image/png;base64," + strings.Repeat("A", 1<<20)
	reqBody := `{"messages":[{"content":"` + secret + `"},{"image_url":"` + img + `"}]}`
	longResp := `{"error":"too large ` + strings.Repeat("x", 64<<10) + `"}`
	tr := &plannerLogTransport{inner: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		_, _ = io.ReadAll(r.Body)
		return &http.Response{StatusCode: 413, Body: io.NopCloser(strings.NewReader(longResp)), Header: http.Header{}}, nil
	})}
	req, _ := http.NewRequest(http.MethodPost, "https://up.example/v1/chat/completions", strings.NewReader(reqBody))
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != longResp {
		t.Fatal("调用方拿到的响应体必须完整")
	}
	out := buf.String()
	if strings.Contains(out, secret) || strings.Contains(out, "base64,AAAA") {
		t.Fatalf("日志里不得出现请求体内容: %.200s", out)
	}
	if !strings.Contains(out, "status=413") {
		t.Fatalf("日志应记录状态码: %.200s", out)
	}
	if len(out) > 8<<10 {
		t.Fatalf("日志应截断，实际 %d 字节", len(out))
	}
}

func TestPlannerLogTransport_TransportErrorDoesNotLogRequestBody(t *testing.T) {
	buf := capturePlannerLog(t)
	tr := &plannerLogTransport{inner: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed")
	})}
	req, _ := http.NewRequest(http.MethodPost, "https://up.example/v1/chat/completions", strings.NewReader(`{"content":"私密"}`))
	_, _ = tr.RoundTrip(req)
	if strings.Contains(buf.String(), "私密") || !strings.Contains(buf.String(), "dial failed") {
		t.Fatalf("日志不符合要求: %s", buf.String())
	}
}

// 响应体里也可能回显 data URI（部分上游会把非法参数原样回显），截断前先折叠。
func TestRedactForLog(t *testing.T) {
	in := `{"error":"bad image data:image/png;base64,` + strings.Repeat("B", 500) + `"}`
	got := redactForLog([]byte(in), 4096)
	if strings.Contains(got, strings.Repeat("B", 100)) || !strings.Contains(got, "data:…(") {
		t.Fatalf("data URI 应被折叠: %s", got)
	}
	long := strings.Repeat("z", 10000)
	if got := redactForLog([]byte(long), 100); len(got) > 160 || !strings.Contains(got, "truncated") {
		t.Fatalf("应截断: %d %q", len(got), got[len(got)-40:])
	}
}
