package http

import nethttp "net/http"

// localUserID 是桌面端唯一的本地用户。
const localUserID int64 = 1

// agentUserID 返回 Agent 相关请求的用户 id。本地身份中间件（P1b）会把 1 写进 context；
// WS 和测试等没经过该中间件的路径回落到 1——桌面端不存在"未登录"或"别的用户"。
func agentUserID(r *nethttp.Request) int64 {
	if uid := userIDFromContext(r.Context()); uid > 0 {
		return uid
	}
	return localUserID
}
