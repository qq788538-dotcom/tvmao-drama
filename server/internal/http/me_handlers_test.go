package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/users"
)

func TestMeReturnsLocalAdminWithLiveStorage(t *testing.T) {
	conn := dbtest.Open(t)
	conn.Exec(`INSERT INTO assets (user_id, object_key, url, size_bytes, mime_type) VALUES (1, 'k', 'media://k', 123, 'image/png')`)
	h := NewMeHandler(users.NewSQLiteRepository(conn))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	h.Me(rec, req.WithContext(withUserID(req.Context(), LocalUserID)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	want := map[string]any{
		"id": float64(1), "email": "local@miaoju.desktop", "role": "admin", "balanceCredits": float64(0),
		"storageQuotaBytes": float64(1125899906842624), "storageUsedBytes": float64(123),
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s=%v want %v", k, body[k], v)
		}
	}
}

func TestMeUnknownUserReturns401(t *testing.T) {
	h := NewMeHandler(users.NewSQLiteRepository(dbtest.Open(t)))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	h.Me(rec, req.WithContext(withUserID(req.Context(), 99)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rec.Code)
	}
}
