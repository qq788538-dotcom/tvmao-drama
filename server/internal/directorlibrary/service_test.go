package directorlibrary

import (
	"errors"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/directormodels"
)

// TestValidateUpload 钉住「与 directormodels 同一套三扩展名约束」——本包的
// ValidateUpload 是别名，谁要是改成自己的实现且放宽/收紧了清单，这里会红。
func TestValidateUpload(t *testing.T) {
	for _, name := range []string{"a.fbx", "b.obj", "c.glb", "C.GLB"} {
		if err := ValidateUpload(name, 1024); err != nil {
			t.Fatalf("ValidateUpload(%q) 应放行, got %v", name, err)
		}
	}
	// .gltf 是多文件格式，单文件上传必缺贴图——全局库与用户库同规则拒绝。
	for _, name := range []string{"d.gltf", "e.stl", "f"} {
		if err := ValidateUpload(name, 1024); err == nil {
			t.Fatalf("ValidateUpload(%q) 应拒绝", name)
		}
	}
	if err := ValidateUpload("big.fbx", directormodels.MaxFileSizeBytes+1); err == nil {
		t.Fatal("超 30MB 应拒绝")
	}
}

func TestValidateThumbUpload(t *testing.T) {
	if err := ValidateThumbUpload("image/png", 1024); err != nil {
		t.Fatalf("png 应放行, got %v", err)
	}
	if err := ValidateThumbUpload("application/octet-stream", 1024); !errors.Is(err, ErrThumbNotImage) {
		t.Fatalf("非图片 MIME 应报 ErrThumbNotImage, got %v", err)
	}
	if err := ValidateThumbUpload("image/jpeg", MaxThumbSizeBytes+1); !errors.Is(err, ErrThumbTooLarge) {
		t.Fatalf("超 5MB 应报 ErrThumbTooLarge, got %v", err)
	}
	// 恰好 5MB 是合法边界（契约是「>5MB → 400」）。
	if err := ValidateThumbUpload("image/webp", MaxThumbSizeBytes); err != nil {
		t.Fatalf("恰好 5MB 应放行, got %v", err)
	}
}

// fakeRepo 只实现 DeleteCategory 语义用到的两个方法，其余误调直接 panic。
type fakeRepo struct {
	Repository
	count   int64
	deleted []int64
}

func (f *fakeRepo) CountModels(int64) (int64, error) { return f.count, nil }
func (f *fakeRepo) DeleteCategory(id int64) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func TestDeleteCategory_NotEmptyRejected(t *testing.T) {
	repo := &fakeRepo{count: 3}
	svc := &Service{Repo: repo}
	err := svc.DeleteCategory(1)
	var notEmpty *ErrCategoryNotEmpty
	if !errors.As(err, &notEmpty) {
		t.Fatalf("非空分类应报 ErrCategoryNotEmpty, got %v", err)
	}
	// 409 契约要展示条数，错误必须携带 CountModels 的原值。
	if notEmpty.Models != 3 {
		t.Fatalf("Models=%d, want 3", notEmpty.Models)
	}
	if len(repo.deleted) != 0 {
		t.Fatal("非空分类不应触达 repo.DeleteCategory")
	}
}

func TestDeleteCategory_EmptyDeletes(t *testing.T) {
	repo := &fakeRepo{count: 0}
	svc := &Service{Repo: repo}
	if err := svc.DeleteCategory(9); err != nil {
		t.Fatalf("空分类应删除成功, got %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 9 {
		t.Fatalf("应删 id=9, got %v", repo.deleted)
	}
}

// TestReferenceNeedles 钉住别名指向 directormodels 的同一套实现。
func TestReferenceNeedles(t *testing.T) {
	got := ReferenceNeedles("lib/2026/ab12_chair.glb")
	if len(got) != 2 || got[0] != `media://lib/2026/ab12_chair.glb"` || got[1] != `media://lib/2026/ab12_chair.glb\` {
		t.Fatalf("ReferenceNeedles=%q", got)
	}
}
