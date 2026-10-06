package agent

// ToolRegistry 是每个具体工具 registry 都要实现的契约。Phase 1 的运行时实现是
// registry_db.go 里的 DBRegistry，它反映 agent_tools 里已发布的行（在 admin 写入
// 时 + 定期安全轮询时刷新）。测试用自己包内的 stub 替换它。
type ToolRegistry interface {
	Snapshot() []Tool
	ByName(name string) (Tool, bool)
	// DisplayName 返回工具的面向用户名称（后台配置的 display_name 或内置工具的
	// 中文名），查不到返回空串——调用方（对话 UI）自行回退到内部名。
	DisplayName(name string) string
}
