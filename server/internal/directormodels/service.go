package directormodels

import (
	"errors"
	"path/filepath"
	"strings"
)

const (
	// UserModelLimit 是每用户模型条数上限。约定定成硬编码常量：最初走
	// settings 键可调，但「调上限」从没有真实发生过——为不存在的运维场景留
	// 配置口子是过度设计（feedback_no_overengineering）。要调就改这里发版。
	UserModelLimit = 30
	// MaxFileSizeBytes 是单文件上限（30MB），服务端强校验——前端的大小提示
	// 只是体验层，挡不住直调 API。
	MaxFileSizeBytes = 30 << 20
)

// 校验失败的哨兵错误。handler 统一映射为 400，message 直接面向用户展示。
var (
	ErrUnsupportedExt = errors.New("仅支持 .fbx / .obj / .glb 模型文件")
	ErrFileTooLarge   = errors.New("模型文件不能超过 30MB")
)

// ValidateUpload 做上传前的静态校验：扩展名（不区分大小写）+ 大小。
// 只认扩展名不嗅探内容——FBX/OBJ/GLB 浏览器普遍报 application/octet-stream，
// MIME 不可依赖；内容合不合法交给前端 3D 加载器在渲染时反馈。
//
// .glb 放行而 .gltf 拒绝：GLB 是 glTF 的单文件二进制封装（几何/材质/贴图
// 打进一个文件），单文件上传即完整；.gltf 是多文件格式（JSON 主文件外挂
// .bin 与贴图），单文件上传必缺贴图，加载出来就是白模——所以错误信息里
// 明确只列 .fbx/.obj/.glb 三种，不给 .gltf 留模糊空间。
func ValidateUpload(filename string, sizeBytes int64) error {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".fbx", ".obj", ".glb":
	default:
		return ErrUnsupportedExt
	}
	if sizeBytes > MaxFileSizeBytes {
		return ErrFileTooLarge
	}
	return nil
}

// Service 把「上限判定」收在包内，handler 只拿结果分支响应码——判定规则
// 可以在本包用 fake 单测，不用起 HTTP。
type Service struct {
	Repo Repository
}

// CanCreate 判定该用户是否还允许新建模型。返回上限常量（达上限时 409 响应体
// 要带给前端展示）与是否放行。count 与后续 Insert 之间存在 TOCTOU 窗口——
// 可接受：并发上传最多让用户短暂超出上限一两条，不会无限超出，因为每个请求
// 都要过这道关卡（同 library 上传的配额预检取舍）。
func (s *Service) CanCreate(userID int64) (limit int, ok bool, err error) {
	count, err := s.Repo.CountByUser(userID)
	if err != nil {
		return UserModelLimit, false, err
	}
	return UserModelLimit, count < UserModelLimit, nil
}
