package http

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestCLIPing(t *testing.T) {
	h := NewRouter(Deps{Auth: cliTestAuth(), DataDir: t.TempDir(), Version: "9.9.9"})
	req := httptest.NewRequest("GET", "/api/v1/cli/ping", nil)
	req.Header.Set("Authorization", cliBearer())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var got map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["app"] != "miaoju-desktop" || got["version"] != "9.9.9" {
		t.Fatalf("got=%v", got)
	}
}
