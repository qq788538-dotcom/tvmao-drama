// P1c（生成管线）尚未合并时的契约类型：只含后处理 handler 用到的符号。
// Task 的字段与 P1c 计划一致（参考仓 generation/repository.go:11-47 去掉 DramaProjectID、
// DramaSegmentID、ActorUserID 三个字段），错误文案照抄参考仓 repository.go 与 storage_quota.go:14。
//
// P1c 合并时它的 repository.go / storage_quota.go 会重复定义这些符号，编译失败即提醒：
// 删除本文件即可，canvas_postprocess_handlers.go 不需要改。
package generation

import (
	"encoding/json"
	"errors"
	"time"
)

type Task struct {
	ID               int64           `json:"id"`
	UserID           int64           `json:"userId"`
	ProjectID        *int64          `json:"projectId,omitempty"`
	ModelID          string          `json:"modelId"`
	Status           string          `json:"status"`
	OutputKind       string          `json:"outputKind,omitempty"`
	UpstreamTaskID   string          `json:"upstreamTaskId,omitempty"`
	Input            json.RawMessage `json:"input"`
	Output           json.RawMessage `json:"output,omitempty"`
	FrozenCents      int64           `json:"frozenCents"`
	CostCents        *int64          `json:"costCents,omitempty"`
	AuditFlag        string          `json:"auditFlag,omitempty"`
	Error            string          `json:"error,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
	Sink             json.RawMessage `json:"-"`
	UpstreamCalledAt *time.Time      `json:"-"`
	Attempt          int             `json:"-"`
	NextRetryAt      *time.Time      `json:"-"`
}

var ErrNotFound = errors.New("task not found")

var ErrStorageQuotaExceeded = errors.New("存储空间已满，请清理后重试再生成")
