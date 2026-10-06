//go:build p1dstub

// P1d 与 P1c 并发开发用的契约桩：只含后处理 handler 用到的符号，字段与文案照抄参考仓
// generation/repository.go:11-47 与 storage_quota.go:14。只在 -tags p1dstub 下编译；
// P1c 合并后由 P1d Task 8 删除本文件。
package generation

import (
	"encoding/json"
	"errors"
)

type Task struct {
	ID      int64           `json:"id"`
	UserID  int64           `json:"userId"`
	ModelID string          `json:"modelId"`
	Status  string          `json:"status"`
	Output  json.RawMessage `json:"output,omitempty"`
	Error   string          `json:"error,omitempty"`
}

var ErrNotFound = errors.New("task not found")

var ErrStorageQuotaExceeded = errors.New("存储空间已满，请清理后重试再生成")
