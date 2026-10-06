package http

import "encoding/json"

// mediaRefSigner 是 P1b *MediaSigner 的窄子集（参考仓 http/media_signer.go 同名方法），
// 生成链路的 handler 只依赖它，P1b 合并前可用 fake 测试。
type mediaRefSigner interface {
	Sign(s string) string
	SignJSON(raw json.RawMessage) json.RawMessage
	Canonicalize(s string) string
}
