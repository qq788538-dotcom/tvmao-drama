package localauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef"

func newHandler() http.Handler {
	cfg := Config{Token: testToken, AllowedOrigins: []string{"tauri://localhost"}}
	return Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

func do(t *testing.T, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, req)
	return rec
}

func TestRejectsMissingToken(t *testing.T) {
	if got := do(t, "GET", "/x", nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", got)
	}
}

func TestRejectsWrongToken(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{"Authorization": "Bearer wrong"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

func TestAcceptsBearerToken(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{"Authorization": "Bearer " + testToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
}

// <img>/<video> 发的请求带不了 Authorization 头，只能走 query。
func TestAcceptsQueryToken(t *testing.T) {
	if got := do(t, "GET", "/x?token="+testToken, nil).Code; got != http.StatusOK {
		t.Fatalf("code = %d, want 200", got)
	}
}

// 本机其他网页即使猜中 token 也必须被挡住。
func TestRejectsForeignOriginEvenWithToken(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{
		"Authorization": "Bearer " + testToken,
		"Origin":        "https://evil.example",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

func TestAllowedOriginGetsCORSHeaders(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{
		"Authorization": "Bearer " + testToken,
		"Origin":        "tauri://localhost",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "tauri://localhost" {
		t.Fatalf("Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got == "" {
		t.Fatal("缺少 Expose-Headers，前端读不到 Content-Range")
	}
}

// 预检请求不带 token，白名单 Origin 要放行。
func TestPreflightAllowedOrigin(t *testing.T) {
	rec := do(t, "OPTIONS", "/x", map[string]string{
		"Origin":                        "tauri://localhost",
		"Access-Control-Request-Method": "POST",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatal("预检缺少 Allow-Headers")
	}
}

func TestPreflightForeignOrigin(t *testing.T) {
	rec := do(t, "OPTIONS", "/x", map[string]string{"Origin": "https://evil.example"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

// 空 token 配置是启动 bug，绝不能变成"任何请求都放行"。
func TestEmptyConfiguredTokenRejectsEverything(t *testing.T) {
	h := Middleware(Config{Token: ""})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x?token=", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

// 裸 OPTIONS（不带 Origin，不是 CORS 预检）不能绕过 token。
func TestBareOptionsRequiresToken(t *testing.T) {
	if got := do(t, "OPTIONS", "/x", nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", got)
	}
}

const testMediaToken = "fedcba9876543210fedcba9876543210"

func doWith(t *testing.T, cfg Config, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	h := Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func mediaCfg() Config {
	return Config{Token: testToken, MediaToken: testMediaToken, AllowedOrigins: []string{"tauri://localhost"}}
}

// P0 遗留：TrimPrefix 不校验前缀，"Basic xxx" 会挡住 ?token= 回退。
func TestNonBearerAuthorizationFallsBackToQuery(t *testing.T) {
	rec := do(t, "GET", "/x?token="+testToken, map[string]string{"Authorization": "Basic Zm9vOmJhcg=="})
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
}

func TestAuthorizationWithoutBearerPrefixRejected(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{"Authorization": testToken})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

// 媒体 URL 会出现在 DOM、日志和 Agent 提示词里：它带的 token 只能读媒体，不能调接口。
func TestMediaTokenOnlyOpensMediaReads(t *testing.T) {
	cfg := mediaCfg()
	cases := []struct {
		method, target string
		headers        map[string]string
		want           int
	}{
		{"GET", "/media/u1/a.png?token=" + testMediaToken, nil, http.StatusOK},
		{"HEAD", "/media/u1/a.png?token=" + testMediaToken, nil, http.StatusOK},
		{"GET", "/api/v1/ping?token=" + testMediaToken, nil, http.StatusUnauthorized},
		{"POST", "/media/u1/a.png?token=" + testMediaToken, nil, http.StatusUnauthorized},
		{"GET", "/api/v1/projects", map[string]string{"Authorization": "Bearer " + testMediaToken}, http.StatusUnauthorized},
		{"GET", "/mediax/a.png?token=" + testMediaToken, nil, http.StatusUnauthorized},
		{"GET", "/media/u1/a.png?token=" + testToken, nil, http.StatusOK}, // 接口 token 也能读媒体
	}
	for _, c := range cases {
		if got := doWith(t, cfg, c.method, c.target, c.headers).Code; got != c.want {
			t.Errorf("%s %s: code = %d, want %d", c.method, c.target, got, c.want)
		}
	}
}

func cliCfg() Config {
	return Config{
		Token:          "web-token-0123456789abcdef0123456789",
		MediaToken:     "media-token-0123456789abcdef01234567",
		CLIToken:       "cli-token-0123456789abcdef012345678",
		AllowedOrigins: []string{"http://tauri.localhost"},
	}
}

// spec 3.3：CLI 路由只认 Bearer CLI token；CLI token 不能用于其他路由。
func TestCLITokenIsolation(t *testing.T) {
	cfg := cliCfg()
	cases := []struct {
		name    string
		method  string
		target  string
		headers map[string]string
		want    int
	}{
		{"cli 路由 + CLI token", "GET", "/api/v1/cli/ping", map[string]string{"Authorization": "Bearer " + cfg.CLIToken}, 200},
		{"cli 路由 + 网页 token", "GET", "/api/v1/cli/ping", map[string]string{"Authorization": "Bearer " + cfg.Token}, 401},
		{"cli 路由 + 媒体 token", "GET", "/api/v1/cli/ping", map[string]string{"Authorization": "Bearer " + cfg.MediaToken}, 401},
		{"cli 路由 + 查询参数 token", "GET", "/api/v1/cli/ping?token=" + cfg.CLIToken, nil, 401},
		{"cli 路由 + 非 Bearer", "GET", "/api/v1/cli/ping", map[string]string{"Authorization": "Basic " + cfg.CLIToken}, 401},
		{"cli 路由 + 白名单 Origin 也拒绝", "GET", "/api/v1/cli/ping", map[string]string{"Authorization": "Bearer " + cfg.CLIToken, "Origin": "http://tauri.localhost"}, 403},
		{"cli 路由 + 预检也拒绝", "OPTIONS", "/api/v1/cli/ping", map[string]string{"Origin": "http://tauri.localhost", "Access-Control-Request-Method": "GET"}, 403},
		{"网页路由 + CLI token", "GET", "/api/v1/projects", map[string]string{"Authorization": "Bearer " + cfg.CLIToken}, 401},
		{"网页路由 + CLI token 查询参数", "GET", "/api/v1/projects?token=" + cfg.CLIToken, nil, 401},
		{"媒体路由 + CLI token", "GET", "/media/a.png?token=" + cfg.CLIToken, nil, 401},
		{"网页路由 + 网页 token 不受影响", "GET", "/api/v1/projects", map[string]string{"Authorization": "Bearer " + cfg.Token}, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := doWith(t, cfg, c.method, c.target, c.headers)
			if rec.Code != c.want {
				t.Fatalf("status=%d want %d", rec.Code, c.want)
			}
		})
	}
}

func TestCLIRouteRejectsAllWhenCLITokenEmpty(t *testing.T) {
	cfg := cliCfg()
	cfg.CLIToken = ""
	rec := doWith(t, cfg, "GET", "/api/v1/cli/ping", map[string]string{"Authorization": "Bearer "})
	if rec.Code != 401 {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

func TestEmptyMediaTokenNeverMatches(t *testing.T) {
	cfg := Config{Token: testToken, AllowedOrigins: []string{"tauri://localhost"}}
	if got := doWith(t, cfg, "GET", "/media/u1/a.png?token=", nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", got)
	}
}

// 沙箱 iframe、file:// 页面发出的请求 Origin 是字面量 "null"。
func TestOriginNullRejected(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{"Authorization": "Bearer " + testToken, "Origin": "null"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

func TestWrongTokenWithAllowedOriginRejected(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{"Authorization": "Bearer wrong", "Origin": "tauri://localhost"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "tauri://localhost" {
		t.Fatal("白名单来源的 401 也要带 CORS 头，前端才能读到状态码")
	}
}

func TestPreflightForeignOriginRejectedEvenWithToken(t *testing.T) {
	rec := do(t, "OPTIONS", "/x", map[string]string{
		"Origin":                        "https://evil.example",
		"Access-Control-Request-Method": "POST",
		"Authorization":                 "Bearer " + testToken,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

func TestPreflightSetsMaxAge(t *testing.T) {
	rec := do(t, "OPTIONS", "/x", map[string]string{"Origin": "tauri://localhost", "Access-Control-Request-Method": "PATCH"})
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Max-Age") != "600" {
		t.Fatalf("code=%d max-age=%q", rec.Code, rec.Header().Get("Access-Control-Max-Age"))
	}
}

// 无 Origin 的请求（如不带 crossOrigin 的 <img>）也必须回 Vary: Origin，
// 否则缓存会把无 ACAO 的响应复用给之后带 Origin 的 CORS 请求。
func TestNoOriginStillVaryOrigin(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{"Authorization": "Bearer " + testToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if got := rec.Header().Values("Vary"); !strings.Contains(strings.Join(got, ","), "Origin") {
		t.Fatalf("Vary = %v, want contains Origin", got)
	}
}

// DNS 重绑定：攻击者域名解析到 127.0.0.1 后的同源请求不带 Origin，Host 却是攻击者域名，
// 哪怕带着（可能已泄露的）媒体 token 也要挡掉。
func TestHostAllowlistBlocksDNSRebinding(t *testing.T) {
	cfg := mediaCfg()
	cfg.AllowedHosts = []string{"127.0.0.1:5000", "localhost:5000"}
	h := Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	cases := []struct {
		host string
		want int
	}{
		{"127.0.0.1:5000", http.StatusOK},
		{"LOCALHOST:5000", http.StatusOK},
		{"evil.example:5000", http.StatusForbidden},
		{"127.0.0.1:5001", http.StatusForbidden},
		{"", http.StatusForbidden},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/media/u1/a.png?token="+testMediaToken, nil)
		req.Host = c.host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("Host %q: code = %d, want %d", c.host, rec.Code, c.want)
		}
	}
}
