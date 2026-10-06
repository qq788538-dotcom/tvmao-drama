// 运维专用 UI（画布侧边栏 → 工作流 JSON 导入导出）的权限门控。
//
// 参考仓按「当前用户是不是超管」放行；桌面端固定单个本地用户（spec X4），
// 这些运维入口一律隐藏（spec S10 不做项目导入导出、F10 隐藏 JSON 导入导出）。
export function useIsSuperAdmin(): boolean {
  return false;
}
