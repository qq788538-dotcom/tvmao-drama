package agent

import (
	"context"
	"errors"
)

// LLMMediaResolver 把要发给上游大模型的媒体地址换成上游拿得到的形式（spec S12 接缝）。
// 桌面端的媒体都在本机，地址是 http://127.0.0.1:<端口>/…，上游访问不到。
// kind 取 "image" | "video" | "audio"。实现见 media_llm_local.go；nil 表示不改写（参考仓行为）。
type LLMMediaResolver interface {
	ResolveForLLM(ctx context.Context, kind, url string) (string, error)
}

// MediaPublisher 把本机媒体发布到用户配置的对象存储（*objectstore.Publisher 满足）。
type MediaPublisher interface {
	Configured() bool
	Publish(ctx context.Context, mediaKey string) (string, error)
}

// ErrMediaNeedsPublicURL：本机视频/音频需要对象存储才能交给上游，但用户还没配置。
var ErrMediaNeedsPublicURL = errors.New("未配置对象存储：视频和音频需要上传到你自己的对象存储，请在 设置 → 配置 → 对象存储 中填写")
