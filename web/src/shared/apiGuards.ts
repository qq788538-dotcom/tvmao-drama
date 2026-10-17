// 全局写入拦截标记，由 axios 请求 interceptor 消费。
//
// 为什么单独拆一个模块而不是从 canvas store 读：api.ts 比 canvas store
// 层级更低，不应该依赖它。相反，是 canvas store 的 `setReadOnly` action
// 调这里的 `setApiWritesBlocked`。api.ts 和 canvas/store.ts 都依赖这个
// lib 模块——绝不会反过来。
//
// 没有放行名单：桌面端没有分享克隆、团队观看「接管编辑」、团队身份自愈这些功能，
// 只读态下写请求一律拦住。
let writesBlocked = false;

const WRITE_METHODS = new Set(['post', 'put', 'patch', 'delete']);

export function setApiWritesBlocked(v: boolean): void {
  writesBlocked = v;
}

export function isApiWriteAllowed(method: string | undefined, url: string | undefined): boolean {
  void url;
  if (!writesBlocked) return true;
  return !WRITE_METHODS.has((method ?? 'get').toLowerCase());
}
