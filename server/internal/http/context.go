package http

import "context"

type contextKey string

const (
	projectIDContextKey contextKey = "projectID"
	userIDContextKey    contextKey = "userID"
	userRoleContextKey  contextKey = "userRole"
	// actorUserIDContextKey 存「团队身份下的真人成员 id」。与 userIDContextKey 并存
	// 而不是替换它：团队身份下 userID 是影子用户（资产归属），actor 只回答
	// 「这一笔是谁干的」——审计与额度用它，业务查询一律不许用。
	actorUserIDContextKey contextKey = "actorUserID"
)

func withProjectID(ctx context.Context, projectID int64) context.Context {
	return context.WithValue(ctx, projectIDContextKey, projectID)
}

func projectIDFromContext(ctx context.Context) int64 {
	value, _ := ctx.Value(projectIDContextKey).(int64)
	return value
}

func withUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userIDContextKey, userID)
}

func userIDFromContext(ctx context.Context) int64 {
	value, _ := ctx.Value(userIDContextKey).(int64)
	return value
}

func withActorUserID(ctx context.Context, actorUserID int64) context.Context {
	return context.WithValue(ctx, actorUserIDContextKey, actorUserID)
}

// actorUserIDFromContext 返回真人成员 id；0 = 调用方是个人身份（非团队）。
func actorUserIDFromContext(ctx context.Context) int64 {
	value, _ := ctx.Value(actorUserIDContextKey).(int64)
	return value
}

func withUserRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, userRoleContextKey, role)
}

func userRoleFromContext(ctx context.Context) string {
	value, _ := ctx.Value(userRoleContextKey).(string)
	return value
}
