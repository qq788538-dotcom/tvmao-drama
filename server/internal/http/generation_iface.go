package http

import "encoding/json"

// mediaRefSigner 是 *MediaSigner 的窄子集，
// 生成链路的 handler 只依赖它，测试可用 fake。
type mediaRefSigner interface {
	Sign(s string) string
	SignJSON(raw json.RawMessage) json.RawMessage
	Canonicalize(s string) string
}
