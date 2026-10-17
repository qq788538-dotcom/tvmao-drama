package http

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
)

// MediaURLExpiry 是传给 storage.SignedURL 的有效期参数。本地 URL 不过期（有效性由每次启动的
// 媒体 token 决定），保留这个常量只为满足 SignedURL 的参数。
const MediaURLExpiry = 24 * time.Hour

// MediaSigner 在 API 边界上，于 `media://<key>`（canonical 的 DB 形式）和 sidecar 本地媒体 URL
// （出口形式 http://127.0.0.1:<端口>/media/<key>?token=…）之间互转。
//
// 不做 LRU 缓存。预签名 URL 才需要缓存来在 20 小时内保持稳定、让浏览器
// 磁盘缓存能命中；本地 URL 本身就是确定的（同一次启动内同一 key 永远同一 URL），换一次启动端口和
// token 都变，缓存跨不了进程，签名也只是字符串拼接。
type MediaSigner struct {
	storage assets.Storage
	ctx     context.Context
}

func NewMediaSigner(storage assets.Storage) *MediaSigner {
	return &MediaSigner{storage: storage, ctx: context.Background()}
}

// Sign 解析单个字符串：如果它是我们的媒体（`media://key`，或任意一次启动发出的本地媒体 URL），
// 返回本次启动的本地 URL；否则原样返回。
func (m *MediaSigner) Sign(s string) string {
	if m == nil || m.storage == nil || s == "" {
		return s
	}
	key, ok := m.storage.ExtractKey(s)
	if !ok || key == "" {
		return s
	}
	url, err := m.storage.SignedURL(m.ctx, key, MediaURLExpiry)
	if err != nil || url == "" {
		return s
	}
	return url
}

// SignThumb 与 Sign 同理，出缩略图 URL；本地存储不生成缩略图，返回的就是原图 URL。
func (m *MediaSigner) SignThumb(s string, maxWidth int) string {
	if m == nil || m.storage == nil || s == "" {
		return s
	}
	key, ok := m.storage.ExtractKey(s)
	if !ok || key == "" {
		return s
	}
	url, err := m.storage.SignedThumbURL(m.ctx, key, MediaURLExpiry, maxWidth)
	if err != nil || url == "" {
		return s
	}
	return url
}

// ObjectKey 把任意形态的媒体引用（media:// 伪 URL、本地 URL）还原成裸 object key。
// 认不出（外部 URL、data: 等）返回空串。
func (m *MediaSigner) ObjectKey(s string) string {
	if m == nil || m.storage == nil || s == "" {
		return ""
	}
	key, ok := m.storage.ExtractKey(s)
	if !ok {
		return ""
	}
	return key
}

// Canonicalize 是逆操作：把本地媒体 URL 转回 `media://<key>` 形式以便持久化。外部 URL 原样返回。
func (m *MediaSigner) Canonicalize(s string) string {
	if m == nil || m.storage == nil || s == "" {
		return s
	}
	if strings.HasPrefix(s, assets.MediaProtocol) {
		return s
	}
	key, ok := m.storage.ExtractKey(s)
	if !ok || key == "" {
		return s
	}
	return assets.MediaProtocol + key
}

// SignJSON 遍历一个 JSON raw message，给找到的每个字符串签名。
func (m *MediaSigner) SignJSON(raw json.RawMessage) json.RawMessage {
	if m == nil {
		return raw
	}
	return assets.TransformJSONRaw(raw, m.Sign)
}

// CanonicalizeJSON 遍历一个 JSON raw message，把每个字符串 canonicalize。
func (m *MediaSigner) CanonicalizeJSON(raw json.RawMessage) json.RawMessage {
	if m == nil {
		return raw
	}
	return assets.TransformJSONRaw(raw, m.Canonicalize)
}
