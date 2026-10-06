// 本文件是 P5 的契约桩：P1b/P1c 尚未合并时让 agent 包能编译。内容逐字取自参考仓
// fea92ab 的同名声明。最终合并时，只要该包已有真实实现（go build 报重复声明），就删除本文件。

package assets

import "time"

type AssetRecord struct {
	ID            int64     `json:"id"`
	UserID        int64     `json:"userId"`
	ProjectID     int64     `json:"projectId,omitempty"`    // 0 → DB 里为 NULL（该 asset 未挂到任何 project）
	SourceTaskID  int64     `json:"sourceTaskId,omitempty"` // 0 → NULL；生成任务的产物会设上它，方便按任务清理
	ObjectKey     string    `json:"objectKey"`
	SourceKind    string    `json:"sourceKind"`
	URL           string    `json:"url"`
	SizeBytes     int64     `json:"sizeBytes"`
	MimeType      string    `json:"mimeType"`
	ContentHash   string    `json:"contentHash,omitempty"` // sha256 十六进制；"" → DB 里为 NULL（历史遗留行）
	CreatedAt     time.Time `json:"createdAt"`
	LibraryItemID int64     `json:"libraryItemId,omitempty"` // 0 → NULL
	DurationMs    int64     `json:"durationMs,omitempty"`    // 视频真实时长（毫秒）；0 → DB NULL。仅视频有值
}
