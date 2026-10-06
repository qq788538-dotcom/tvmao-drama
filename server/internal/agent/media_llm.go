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

// ErrMediaNeedsPublicURL：本机视频/音频目前没有办法交给上游（S12 待定）。
var ErrMediaNeedsPublicURL = errors.New("本机视频和音频暂时无法发送给模型分析：上游需要公网地址，该方案尚未确定")
