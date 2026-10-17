package agent

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// Tool 是每个 agent 工具都实现的本地 interface。精确对齐 eino 的
// tool.InvokableTool，这样一个 Tool 不用转换就能传给 eino 的 ToolCallingChatModel。
type Tool interface {
	Info(ctx context.Context) (*schema.ToolInfo, error)
	InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error)
}

// EmitFunc 让一个工具能把 tool_event artifact 推给当前活跃的 WS client。
type EmitFunc func(Artifact)

type emitCtxKey struct{}

// WithEmit 把一个 EmitFunc 挂到 ctx 上，这样工具在 InvokableRun 期间能把 artifact
// 推给当前活跃的 WS client。
func WithEmit(ctx context.Context, fn EmitFunc) context.Context {
	return context.WithValue(ctx, emitCtxKey{}, fn)
}

// EmitFromContext 返回挂在 ctx 上的 EmitFunc，没挂的话返回一个 noop。可以无条件
// 调用，安全。
func EmitFromContext(ctx context.Context) EmitFunc {
	if fn, ok := ctx.Value(emitCtxKey{}).(EmitFunc); ok && fn != nil {
		return fn
	}
	return func(Artifact) {}
}

// User/Project context 管线——agent loop 每次 Run 都设这些，这样工具能提交
// 归属到正确调用方的生成任务。

type userCtxKey struct{}
type projectCtxKey struct{}

func WithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userCtxKey{}, userID)
}

func UserIDFromContext(ctx context.Context) int64 {
	if v, ok := ctx.Value(userCtxKey{}).(int64); ok {
		return v
	}
	return 0
}

func WithProjectID(ctx context.Context, projectID int64) context.Context {
	return context.WithValue(ctx, projectCtxKey{}, projectID)
}

func ProjectIDFromContext(ctx context.Context) int64 {
	if v, ok := ctx.Value(projectCtxKey{}).(int64); ok {
		return v
	}
	return 0
}
