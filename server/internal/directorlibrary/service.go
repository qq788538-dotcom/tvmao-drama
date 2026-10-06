package directorlibrary

import (
	"errors"
	"fmt"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/directormodels"
)

// 模型文件校验与引用 LIKE 构造直接复用 directormodels 的实现：全局模型库与
// 用户模型库承载的是同一类资产（3D 模型文件、media:// 引用形态），格式/大小
// 约束与转义规则必须一致——各写一份迟早漂移（同构纪律同 canvasinputs 的教训）。
var (
	ValidateUpload       = directormodels.ValidateUpload
	ReferenceLikePattern = directormodels.ReferenceLikePattern
)

const (
	// MaxThumbSizeBytes 是缩略图上限（5MB）。缩略图只是目录里的预览小图，
	// 5MB 已远超合理体积，再大就是误传原始渲染图。
	MaxThumbSizeBytes = 5 << 20
)

// 缩略图校验失败的哨兵错误，handler 映射 400。
var (
	ErrThumbNotImage = errors.New("缩略图仅支持图片文件")
	ErrThumbTooLarge = errors.New("缩略图不能超过 5MB")
)

// ValidateThumbUpload 校验缩略图：MIME image/* + 大小。缩略图与模型文件相反，
// 按 MIME 而不是扩展名判——图片浏览器会带真实 image/* Content-Type，
// 且允许的格式多（png/jpg/webp/…），列扩展名清单反而挂一漏万。
func ValidateThumbUpload(contentType string, sizeBytes int64) error {
	if !strings.HasPrefix(contentType, "image/") {
		return ErrThumbNotImage
	}
	if sizeBytes > MaxThumbSizeBytes {
		return ErrThumbTooLarge
	}
	return nil
}

// ErrCategoryNotEmpty 携带分类下模型条数——409 响应体契约
// {"error":"category_not_empty","models":N} 要把 N 带给前端展示。
type ErrCategoryNotEmpty struct{ Models int64 }

func (e *ErrCategoryNotEmpty) Error() string {
	return fmt.Sprintf("分类下还有 %d 个模型，不能删除", e.Models)
}

// Service 把「非空分类拒删」这条业务规则收在包内（handler 只按错误类型分支
// 响应码），可用 fake repo 单测。其余 CRUD 无业务规则，handler 直连 repo。
type Service struct {
	Repo Repository
}

// DeleteCategory 非空分类拒删：分类被删时下面的模型会连带孤儿化（对象还在
// 存储里却没了目录入口），所以必须先清空/移走模型。Count 与 Delete 之间的
// 并发窗口由 FK ON DELETE RESTRICT 兜底——预检漏掉的并发插入会让 DELETE 直接
// 报 FK 错，宁可 500 也不产生孤儿模型行。
func (s *Service) DeleteCategory(id int64) error {
	n, err := s.Repo.CountModels(id)
	if err != nil {
		return err
	}
	if n > 0 {
		return &ErrCategoryNotEmpty{Models: n}
	}
	return s.Repo.DeleteCategory(id)
}
