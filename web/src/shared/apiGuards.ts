// 全局写入拦截标记，由 axios 请求 interceptor 消费。
//
// 为什么单独拆一个模块而不是从 canvas store 读：api.ts 比 canvas store
// 层级更低，不应该依赖它。相反，是 canvas store 的 `setReadOnly` action
// 调这里的 `setApiWritesBlocked`。api.ts 和 canvas/store.ts 都依赖这个
// lib 模块——绝不会反过来。
let writesBlocked = false;

const WRITE_METHODS = new Set(['post', 'put', 'patch', 'delete']);

// 即便画布处于只读模式也仍可访问的端点。放行的都是「用户主动发起的、
// 明确要脱离只读态」的转换动作，不是对只读画布本身的编辑：
//  1. 分享 viewer 顶栏的「把这个分享工作流克隆到我自己的项目」；
//  2. 团队观看模式的「接管编辑」——观看模式正是靠 readOnly 实现的，
//     抢锁请求自己被这道闸拦住的话，用户永远接管不了（点了没反应）。
//     另一条路是先 setReadOnly(false) 再抢锁，但那会在抢到锁之前把**所有**
//     写请求放行一瞬间，队列里 debounce 中的节点 mutation 会趁机打出去。
//  3. 团队身份失效的自愈换发（`/auth/switch-context`）——它是 401 拦截器自己
//     发的补救请求，跟画布内容无关。观看态下被这道闸拦住的话，自愈第一步就
//     失败：观看中被移出团队 → 画布静默冻住，用户只看到一堆失败的请求。
const ALLOWLIST: RegExp[] = [
  /\/workflows\/by-token\/[^/]+\/clone$/,
  /^\/projects\/\d+\/canvas\/owner\/claim$/,
  /^\/auth\/switch-context$/,
];

export function setApiWritesBlocked(v: boolean): void {
  writesBlocked = v;
}

export function isApiWriteAllowed(method: string | undefined, url: string | undefined): boolean {
  if (!writesBlocked) return true;
  const m = (method ?? 'get').toLowerCase();
  if (!WRITE_METHODS.has(m)) return true;
  return ALLOWLIST.some((re) => re.test(url ?? ''));
}

// 只读模式下用户可能确实是匿名的（没有 token），所以按用户维度的
// /me/* 端点返回 401 是「预期之内」的，而不是「会话过期」。axios 响应
// interceptor 会查这个标记，从而跳过自动跳转登录的行为——调用方的
// useQuery 只是静默报错，UI 展示一个空列表。
export function isApiInReadOnlyMode(): boolean {
  return writesBlocked;
}
