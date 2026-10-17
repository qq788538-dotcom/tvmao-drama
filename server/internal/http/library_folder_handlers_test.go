package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/library"
)

// fakeFoldersRepo 只实现守卫测试所需路径，其余 panic 以免测试静默走错分支。
type fakeFoldersRepo struct {
	folders map[int64]*library.Folder
	// moved 记录 Move 是否被调用及入参，验证守卫放行后确实走到底层移动。
	moved       bool
	movedParent *int64
}

func (f *fakeFoldersRepo) List(userID int64) ([]library.Folder, error) { return nil, nil }
func (f *fakeFoldersRepo) Get(userID, id int64) (*library.Folder, error) {
	if fo, ok := f.folders[id]; ok {
		return fo, nil
	}
	return nil, library.ErrFolderNotFound
}
func (f *fakeFoldersRepo) Create(int64, *int64, string) (int64, error) { panic("unused") }
func (f *fakeFoldersRepo) Rename(int64, int64, string) error {
	panic("guard should reject before rename")
}
func (f *fakeFoldersRepo) Move(userID, id int64, newParentID *int64) error {
	f.moved = true
	f.movedParent = newParentID
	return nil
}
func (f *fakeFoldersRepo) DeleteIfEmpty(int64, int64) error {
	panic("guard should reject before delete")
}
func (f *fakeFoldersRepo) DescendantIDs(int64, int64) ([]int64, error) { return nil, nil }

func newFolderReq(method, path string, body string, folderID string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("folderID", folderID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return withTestUserID(req, 7)
}

// 子目录移回根目录（clearParent）→ 放行，Move 收到 nil parent。
func TestFolderMoveToRootAllowed(t *testing.T) {
	repo := &fakeFoldersRepo{
		folders: map[int64]*library.Folder{
			5: {ID: 5, Name: "曾在子目录下的普通目录"},
		},
	}
	h := NewLibraryFolderHandler(repo, nil, nil, nil, nil)
	rec := httptest.NewRecorder()
	h.Update(rec, newFolderReq(http.MethodPatch, "/library/folders/5", `{"clearParent":true}`, "5"))
	if rec.Code != http.StatusOK {
		t.Fatalf("移回根目录应 200，得 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !repo.moved || repo.movedParent != nil {
		t.Fatalf("移回根目录应调用 Move(nil)，moved=%v parent=%v", repo.moved, repo.movedParent)
	}
}
