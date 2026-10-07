package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

var ErrNotConfigured = errors.New("未配置对象存储：视频和音频需要上传到你自己的对象存储，请在 设置 → 配置 → 对象存储 中填写")

// Client 是对各服务商 SDK 的最小封装。
type Client interface {
	Exists(ctx context.Context, key string) (bool, error)
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	Delete(ctx context.Context, key string) error // 只给连接测试用
}

type ClientFactory func(Config) (Client, error)

// providers 由 tos.go / oss.go 在 init() 里登记。
var providers = map[string]ClientFactory{}

// NewClient 按 Provider 分发。
func NewClient(c Config) (Client, error) {
	f, ok := providers[c.Provider]
	if !ok {
		return nil, fmt.Errorf("不支持的服务商 %q", c.Provider)
	}
	return f(c)
}
