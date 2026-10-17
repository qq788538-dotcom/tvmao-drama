package billing

import "context"

// actorKey 是 ctx 里「真实操作者」的私有 key 类型，避免跨包碰撞（同 dramaScopeKey）。
type actorKey struct{}

// WithActor 把真人成员的 user id 挂到 ctx。桌面端只有一个本地用户、没有团队，
// 保留它只是为了让搬过来的调用方（canvasscript、agent）原样编译；
// actorUserID<=0 或不包 = 个人路径。
func WithActor(ctx context.Context, actorUserID int64) context.Context {
	return context.WithValue(ctx, actorKey{}, actorUserID)
}

// ActorFromContext 取回真实操作者；未设置返回 0（=个人路径）。
func ActorFromContext(ctx context.Context) int64 {
	if v, ok := ctx.Value(actorKey{}).(int64); ok {
		return v
	}
	return 0
}
