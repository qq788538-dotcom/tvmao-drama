//go:build p1cshim

// 本文件只供 P1c 在 P1b 合并前独立编译、测试（go test -tags p1cshim）。
// 内容逐字取自参考仓 assets 包；P1b 合并后默认构建不再包含本文件，Task 10 删除它。
package assets

import (
	"bytes"
	"io"
	"time"
)

type PutObjectInput struct {
	Key         string
	ContentType string
	Body        []byte
	Reader      io.Reader
	Size        int64
}

func (in PutObjectInput) Content() (io.Reader, int64) {
	if in.Body != nil {
		return bytes.NewReader(in.Body), int64(len(in.Body))
	}
	if in.Reader == nil {
		return bytes.NewReader(nil), 0
	}
	return in.Reader, in.Size
}

type StoredObject struct {
	URL  string
	Size int64
}

type AssetRecord struct {
	ID            int64     `json:"id"`
	UserID        int64     `json:"userId"`
	ProjectID     int64     `json:"projectId,omitempty"`
	SourceTaskID  int64     `json:"sourceTaskId,omitempty"`
	ObjectKey     string    `json:"objectKey"`
	SourceKind    string    `json:"sourceKind"`
	URL           string    `json:"url"`
	SizeBytes     int64     `json:"sizeBytes"`
	MimeType      string    `json:"mimeType"`
	ContentHash   string    `json:"contentHash,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	LibraryItemID int64     `json:"libraryItemId,omitempty"`
	DurationMs    int64     `json:"durationMs,omitempty"`
}

const MediaProtocol = "media://"

func WalkStrings(v any, transform func(string) string) any {
	switch x := v.(type) {
	case string:
		return transform(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = WalkStrings(val, transform)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = WalkStrings(val, transform)
		}
		return out
	default:
		return v
	}
}
