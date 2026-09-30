package localauth

import (
	"net/http"
	"net/http/httptest"
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
