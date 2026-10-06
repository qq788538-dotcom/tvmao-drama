//go:build p1dstub

// P1d 与 P1c 并发开发用的契约桩：字段照抄参考仓 canvasgen/service.go:90-101 的 EnqueueInput。
// 只在 -tags p1dstub 下编译；P1c 合并后由 P1d Task 8 删除本文件。
package canvasgen

type EnqueueInput struct {
	UserID       int64
	ProjectID    int64
	NodeID       string
	ModelID      string
	Inputs       map[string]any
	PresetKey    string
	PresetParams map[string]string
}
