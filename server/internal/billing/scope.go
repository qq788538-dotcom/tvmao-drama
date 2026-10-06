package billing

import "context"

// dramaScopeKey 是 ctx 里 drama 归属(项目/片段)的私有 key 类型，避免跨包碰撞。
type dramaScopeKey struct{}

type dramaScope struct{ projectID, segmentID int64 }

// WithDramaScope 把 drama 项目/片段归属挂到 ctx。仅 drama 相关计费调用链需要包它；
// 其余(画布/agent/chat)不包，scope 读出来即 0/0 → 账本两列写 NULL。
// 0 表示无(非法 id)，故 projectID=0 时整体视为无归属。
func WithDramaScope(ctx context.Context, projectID, segmentID int64) context.Context {
	return context.WithValue(ctx, dramaScopeKey{}, dramaScope{projectID, segmentID})
}

// dramaScopeFromCtx 取回归属；未设置返回 0,0。
func dramaScopeFromCtx(ctx context.Context) (projectID, segmentID int64) {
	if v, ok := ctx.Value(dramaScopeKey{}).(dramaScope); ok {
		return v.projectID, v.segmentID
	}
	return 0, 0
}

// nullableID 把 0 转成 nil(写 NULL)、非 0 原样返回，供账本 INSERT 的 drama 列绑定。
func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
