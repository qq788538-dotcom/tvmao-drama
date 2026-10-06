package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestLocalIdentityInjectsLocalAdmin(t *testing.T) {
	var uid, actor int64 = 0, -1
	var role string
	h := LocalIdentity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid = userIDFromContext(r.Context())
		role = userRoleFromContext(r.Context())
		actor = actorUserIDFromContext(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/me", nil))
	if uid != 1 || role != "admin" || actor != 0 {
		t.Fatalf("uid=%d role=%q actor=%d；want 1/admin/0（个人身份，团队分支不可达）", uid, role, actor)
	}
}

func TestProjectFromURL(t *testing.T) {
	var got int64
	r := chi.NewRouter()
	r.With(ProjectFromURL).Get("/p/{projectID}", func(w http.ResponseWriter, r *http.Request) {
		got = projectIDFromContext(r.Context())
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/p/42", nil))
	if rec.Code != http.StatusOK || got != 42 {
		t.Fatalf("code=%d got=%d", rec.Code, got)
	}
	for _, bad := range []string{"/p/abc", "/p/0", "/p/-3"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", bad, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: code=%d want 400", bad, rec.Code)
		}
	}
}
