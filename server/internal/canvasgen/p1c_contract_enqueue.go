// P1c（生成管线）尚未合并时的契约类型：字段照抄参考仓 canvasgen/service.go:90-101 的
// EnqueueInput，P1c 计划不改它。P1c 合并时 canvasgen/service.go 会重复定义，删除本文件即可。
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
