package http

import (
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type connResult struct {
	ID      int64  `json:"id"`
	ModelID string `json:"modelId"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func runTestConn(t *testing.T, e *scEnv) map[string]connResult {
	t.Helper()
	code, body := e.do("POST", "/models/test-connection", nil)
	if code != 200 {
		t.Fatalf("code=%d body=%s", code, body)
	}
	resp := decodeTo[struct {
		Results []connResult `json:"results"`
	}](t, body)
	out := map[string]connResult{}
	for _, r := range resp.Results {
		out[r.ModelID] = r
	}
	if len(out) != 4 {
		t.Fatalf("应返回全部 4 个模型（含隐藏/停用），got %d: %s", len(out), body)
	}
	return out
}

// 上游桩：返回给定模型 id 列表，记录请求数与最近一次 Authorization。
func upstreamStub(hits *int32, auth *atomic.Value, ids ...string) *httptest.Server {
	return httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		atomic.AddInt32(hits, 1)
		if auth != nil {
			auth.Store(r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(404)
			return
		}
		var items []string
		for _, id := range ids {
			items = append(items, fmt.Sprintf(`{"id":%q}`, id))
		}
		fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(items, ","))
	}))
}

func TestTestConnection_SuccessAndMissingModel(t *testing.T) {
	e := newSCEnv(t)
	var hits int32
	var auth atomic.Value
	srv := upstreamStub(&hits, &auth, "gpt-image-2", "canvas-gpt-5.5")
	defer srv.Close()
	_ = e.settings.Set("base_domain", srv.URL)
	_ = e.settings.Set("upstream_api_key", "sk-global")

	res := runTestConn(t, e)
	if !res["gpt-image-2"].OK || res["gpt-image-2"].Message != "成功" {
		t.Fatalf("gpt-image-2: %+v", res["gpt-image-2"])
	}
	if res["gpt-image-2"].ID != e.ids["gpt-image-2"] {
		t.Fatalf("id 不符: %+v", res["gpt-image-2"])
	}
	if r := res["old-text"]; r.OK || r.Message != "网关上没有这个模型（old-text）" {
		t.Fatalf("old-text: %+v", r)
	}
	if !res["canvas-gpt-5.5"].OK {
		t.Fatalf("隐藏模型也要测: %+v", res["canvas-gpt-5.5"])
	}
	// 同一上游只请求一次
	if hits != 1 {
		t.Fatalf("同一上游应只请求 1 次，got %d", hits)
	}
	if auth.Load() != "Bearer sk-global" {
		t.Fatalf("auth=%v", auth.Load())
	}
}

func TestTestConnection_Unauthorized(t *testing.T) {
	e := newSCEnv(t)
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":{"message":"无效的令牌"}}`)
	}))
	defer srv.Close()
	_ = e.settings.Set("base_domain", srv.URL)
	_ = e.settings.Set("upstream_api_key", "sk-bad")
	r := runTestConn(t, e)["gpt-image-2"]
	if r.OK || r.Message != "HTTP 401：无效的令牌" {
		t.Fatalf("%+v", r)
	}
}

func TestTestConnection_NetworkFailure(t *testing.T) {
	e := newSCEnv(t)
	srv := httptest.NewServer(nethttp.NotFoundHandler())
	url := srv.URL
	srv.Close() // 端口已关，必然连接失败
	_ = e.settings.Set("base_domain", url)
	_ = e.settings.Set("upstream_api_key", "sk-x")
	r := runTestConn(t, e)["gpt-image-2"]
	if r.OK || !strings.HasPrefix(r.Message, "连接失败：") {
		t.Fatalf("%+v", r)
	}
}

func TestTestConnection_OwnUpstreamIsolated(t *testing.T) {
	e := newSCEnv(t)
	var globalHits, ownHits int32
	var ownAuth atomic.Value
	global := upstreamStub(&globalHits, nil, "canvas-gpt-5.5")
	own := upstreamStub(&ownHits, &ownAuth, "gpt-image-2")
	defer global.Close()
	defer own.Close()
	_ = e.settings.Set("base_domain", global.URL)
	_ = e.settings.Set("upstream_api_key", "sk-global")
	if err := e.models.SetUpstream(e.ids["gpt-image-2"], own.URL, "sk-own"); err != nil {
		t.Fatal(err)
	}
	res := runTestConn(t, e)
	if !res["gpt-image-2"].OK || ownHits != 1 || ownAuth.Load() != "Bearer sk-own" {
		t.Fatalf("own: %+v hits=%d auth=%v", res["gpt-image-2"], ownHits, ownAuth.Load())
	}
	if globalHits != 1 || !res["canvas-gpt-5.5"].OK {
		t.Fatalf("global hits=%d %+v", globalHits, res["canvas-gpt-5.5"])
	}
}

func TestTestConnection_Unconfigured(t *testing.T) {
	e := newSCEnv(t)
	_ = e.settings.Set("base_domain", "")
	_ = e.settings.Set("upstream_api_key", "")
	for id, r := range runTestConn(t, e) {
		if r.OK || r.Message != "未配置上游地址或 API Key" {
			t.Fatalf("%s: %+v", id, r)
		}
	}
}

func TestTestConnection_RedactsKey(t *testing.T) {
	e := newSCEnv(t)
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":{"message":"key sk-secret-123 not allowed"}}`)
	}))
	defer srv.Close()
	_ = e.settings.Set("base_domain", srv.URL)
	_ = e.settings.Set("upstream_api_key", "sk-secret-123")
	code, body := e.do("POST", "/models/test-connection", nil)
	if code != 200 || strings.Contains(body, "sk-secret-123") || !strings.Contains(body, "***") {
		t.Fatalf("code=%d body=%s", code, body)
	}
}
