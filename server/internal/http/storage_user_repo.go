package http

// storageUserRepo 是上传、项目、素材库各 handler 共用的存储用量接口（多个 handler 都要用，
// 所以放在独立文件）。
type storageUserRepo interface {
	AddStorage(id int64, delta int64) error
	// StorageUsage 取 (used, quota) 做上传前的配额预检——记账(AddStorage)与
	// 预检必须来自同一个计数器，否则闸和账各说各话。
	StorageUsage(id int64) (used int64, quota int64, err error)
}
