package assets

import (
	"bytes"
	"context"
	"io"
	"time"
)

// PutObjectInput 两种传法二选一：Body（已在内存的小对象）或 Reader+Size（大文件流式）。
// 加 Reader 是为了 gpt-image 产物从临时文件直接上传——SDK 要写 Content-Length，
// 拿不到长度就得走分片上传，所以 Size 必须显式给。实现方一律经 Content() 取内容。
type PutObjectInput struct {
	Key         string
	ContentType string
	Body        []byte
	Reader      io.Reader
	Size        int64
}

// Content 统一两种传法。Body 非 nil 时优先——老调用方零改动。
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

type Storage interface {
	PutObject(ctx context.Context, input PutObjectInput) (StoredObject, error)
	DeleteObject(ctx context.Context, key string) error
	SignedURL(ctx context.Context, key string, expiry time.Duration) (string, error)
	// SignedThumbURL 出缩略图签名 URL；maxWidth<=0 等同原图。
	// TOS 用 x-tos-process 缩放；MinIO 不支持图片处理，退回原图签名。
	SignedThumbURL(ctx context.Context, key string, expiry time.Duration, maxWidth int) (string, error)
	ExtractKey(urlOrKey string) (string, bool)
	// GetObject 按 key 读取对象内容流（导出打包用）。调用方负责 Close。
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)
	// StaticKey 构造不含时间成分的确定性 key(v5 §5.5):同一入参永远同一 key,
	// persistOutputs 槽位重放覆盖写同一对象,不产生孤儿。
	StaticKey(userID int64, filename string) string
	// NewKey 为一次上传构造新的 object key，会应用上 storage 配置的 Prefix，
	// 这样多个应用能共用一个 bucket 而不冲突。
	// 布局：<prefix>/u<userID>/<year>/<month>/<filename>
	NewKey(userID int64, filename string) string
	// CopyObject 在同一个 bucket 内把已有对象从 `srcKey` 复制到 `dstKey`。
	// 各后端用云原生的 CopyObject，所以不需要下载/重传的往返——同 region 的复制
	// 基本不产生 egress 费用，即使大文件也只需 ~ms。
	// 返回新对象的公网 URL + 大小。
	CopyObject(ctx context.Context, srcKey, dstKey, contentType string) (StoredObject, error)
}
