package http

// storageUserRepo 是上传、项目、素材库各 handler 共用的存储用量接口（参考仓定义在 asset_handlers.go，
// 挪到独立文件是因为多个任务的 handler 并发移植、都要用它）。
type storageUserRepo interface {
	AddStorage(id int64, delta int64) error
	// StorageUsage 取 (used, quota) 做上传前的配额预检——记账(AddStorage)与
	// 预检必须来自同一个计数器，否则闸和账各说各话。
	StorageUsage(id int64) (used int64, quota int64, err error)
}
