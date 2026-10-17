package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/cliapi"
)

// decodeCLIError 解出错误体，供本包各 CLI 测试复用。
func decodeCLIError(t *testing.T, rec *httptest.ResponseRecorder) cliErrorBody {
	t.Helper()
	var env cliErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body: %v (body=%s)", err, rec.Body.String())
	}
	return env.Error
}

func TestWriteCLIErrorRendersStableCode(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cli/projects/1", nil)
	rec := httptest.NewRecorder()
	CLIRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCLIError(w, r, cliapi.New(cliapi.CodeNodeNotFound, "节点不存在"))
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := decodeCLIError(t, rec)
	if body.Code != cliapi.CodeNodeNotFound {
		t.Fatalf("code = %q", body.Code)
	}
	if body.RequestID == "" {
		t.Fatal("requestId 必须非空——用户贴它来定位服务端日志")
	}
	if rec.Header().Get("X-Request-ID") != body.RequestID {
		t.Fatal("响应头与错误体里的 requestId 必须一致")
	}
	if body.Retryable {
		t.Fatal("NODE_NOT_FOUND 不该标可重试")
	}
}

func TestWriteCLIErrorDowngradesUnknownError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	CLIRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCLIError(w, r, errors.New("底层 SQL 炸了"))
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := decodeCLIError(t, rec).Code; got != cliapi.CodeInternal {
		t.Fatalf("code = %q, want INTERNAL", got)
	}
}

func TestCLIRequestIDReusesCallerValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-ID", "req_caller")
	rec := httptest.NewRecorder()
	CLIRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := requestIDFromContext(r.Context()); got != "req_caller" {
			t.Fatalf("context requestId = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") != "req_caller" {
		t.Fatalf("header = %q", rec.Header().Get("X-Request-ID"))
	}
}

func TestWriteCLIErrorSetsRetryAfterForInProgress(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	rec := httptest.NewRecorder()
	CLIRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCLIError(w, r, cliapi.New(cliapi.CodeIdempotencyInProgress, "处理中"))
	})).ServeHTTP(rec, req)

	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("IDEMPOTENCY_IN_PROGRESS 必须带 Retry-After，否则 CLI 只能盲目退避")
	}
	if !decodeCLIError(t, rec).Retryable {
		t.Fatal("IDEMPOTENCY_IN_PROGRESS 应标可重试")
	}
}
