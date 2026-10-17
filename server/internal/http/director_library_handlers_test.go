package http

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/directorlibrary"
)

// directorFakeLibraryRepo 只实现 ListEnabledJoined；嵌入接口满足其余方法（被调用会 panic，正好暴露误用）。
type directorFakeLibraryRepo struct {
	directorlibrary.Repository
	cats []directorlibrary.CategoryWithModels
}

func (f directorFakeLibraryRepo) ListEnabledJoined() ([]directorlibrary.CategoryWithModels, error) {
	return f.cats, nil
}

type directorFakeSigner struct{}

func (directorFakeSigner) Sign(s string) string {
	return "https://signed/" + strings.TrimPrefix(s, "media://")
}

func (directorFakeSigner) SignThumb(s string, w int) string {
	return fmt.Sprintf("https://thumb/%d/%s", w, strings.TrimPrefix(s, "media://"))
}

type directorLibraryOut struct {
	Categories []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Models []struct {
			ID       int64  `json:"id"`
			Name     string `json:"name"`
			FileName string `json:"fileName"`
			URL      string `json:"url"`
			ThumbURL string `json:"thumbUrl"`
		} `json:"models"`
	} `json:"categories"`
}

func TestDirectorLibraryListSignsAndKeepsEmpty(t *testing.T) {
	repo := directorFakeLibraryRepo{cats: []directorlibrary.CategoryWithModels{
		{Category: directorlibrary.Category{ID: 1, Name: "家具"}, Models: []directorlibrary.Model{
			{ID: 10, Name: "椅子", FileName: "chair.glb", ObjectKey: "lib/chair.glb", ThumbObjectKey: "lib/chair.png"},
			{ID: 11, Name: "桌子", FileName: "desk.glb", ObjectKey: "lib/desk.glb"},
		}},
		{Category: directorlibrary.Category{ID: 2, Name: "空分类"}, Models: []directorlibrary.Model{}},
	}}
	rec := httptest.NewRecorder()
	NewDirectorLibraryHandler(repo, directorFakeSigner{}).List(rec, httptest.NewRequest("GET", "/api/v1/director/library", nil))
	var out directorLibraryOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Categories) != 2 {
		t.Fatalf("categories = %+v", out.Categories)
	}
	m := out.Categories[0].Models
	if m[0].URL != "https://signed/lib/chair.glb" || m[0].ThumbURL != "https://thumb/400/lib/chair.png" {
		t.Fatalf("签名不对: %+v", m[0])
	}
	if m[1].ThumbURL != "" {
		t.Fatalf("无缩略图应为空串: %+v", m[1])
	}
	if !strings.Contains(rec.Body.String(), `"models":[]`) {
		t.Fatalf("空分类的 models 必须是 []：%s", rec.Body)
	}
}

// 桌面端内置库为空：必须返回 {"categories":[]} 而不是 null。
func TestDirectorLibraryListEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	NewDirectorLibraryHandler(directorFakeLibraryRepo{}, directorFakeSigner{}).List(rec, httptest.NewRequest("GET", "/api/v1/director/library", nil))
	if strings.TrimSpace(rec.Body.String()) != `{"categories":[]}` {
		t.Fatalf("body = %s", rec.Body)
	}
}
