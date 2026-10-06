import { clearAuthSession, setToken, switchContext } from './api';

// 画布节点剪贴板镜像的 localStorage key。字面量抄自
// modules/canvas/editor/utils/nodeClipboard.ts 的 MIRROR_KEY，不 import 它——
// shared 层不该反向依赖 canvas 模块，两处各自持有这一个字符串常量的耦合可接受
// （key 名一旦要改，nodeClipboard.ts 那边的测试会先炸）。
const CANVAS_CLIPBOARD_MIRROR_KEY = 'tvmao:canvas-node-clipboard';

/**
 * 切换身份（个人 ↔ 团队，或团队 A ↔ 团队 B）。teamId=0 切回个人。
 *
 * 拍板做法：换到新 token 后**整页刷新**回首页，不做 SPA 内导航——
 * 切身份意味着资产/额度归属整个变了，内存里的 react-query 缓存、zustand
 * store 全部挂在旧身份下，逐个手动失效很容易漏一处；用 location.assign
 * 把前端进程整个重启一次更简单也更可靠。
 *
 * 顺带清掉画布节点剪贴板的 localStorage 镜像：它是跨标签页共享的复制/粘贴
 * 缓存，不清的话个人身份下复制的节点内容能在切到团队身份后被粘贴出来，
 * 是一条跨身份的数据泄漏路径。
 *
 * 失败时什么都不做（不换 token、不清缓存、不跳转）——调用方按 rejected
 * promise 处理错误展示，用户仍停留在原身份，不会出现"改了一半"的中间态。
 */
export async function switchIdentity(teamId: number): Promise<void> {
  const { token } = await switchContext(teamId);
  applyIdentityToken(token);
}

/**
 * 换身份的收尾三步：落新 token → 清剪贴板镜像 → 整页重启。
 * 单独导出是因为「死身份自愈」那条路径（见下）要走一模一样的三步，
 * 复制一份的话将来加第四步只会加在其中一处。
 */
function applyIdentityToken(token: string): void {
  setToken(token);
  localStorage.removeItem(CANVAS_CLIPBOARD_MIRROR_KEY);
  window.location.assign('/');
}

/**
 * 死身份自愈：当前团队身份已经失效（团队被解散 / 自己被移出）时，把身份换回
 * 个人，而不是把用户踢下线。
 *
 * WHY（2026-08-18 终审 C1）：团队一旦解散/移出，团队 token 的**每一个**请求都会
 * 拿到 401「团队成员资格已失效」。这跟"会话过期"不是一回事——真人的登录态完全
 * 有效，失效的只是团队这层身份。旧行为是 axios 拦截器清 session 硬跳 /login：
 * 用户点了「解散团队」，终态是被强制重新登录，还得重输密码。
 *
 * 换发本身用的还是那份死 token——后端 switch-context 走 RequireSwitchable，
 * 不校验"当前团队还活着"，正是为这条路径开的口。
 *
 * 自愈失败（换发也 401 了，说明真人这一层的凭据也不行了）才回落到清 session
 * 跳登录页——这是兜底，不是常态。
 *
 * 返回值 = 换发是否成功。**不抛**（调用方是 axios 拦截器里的 fire-and-forget），
 * 但成功与失败必须区分得出来：拦截器的单飞标志靠它决定要不要放开下一次尝试
 * （见 api.ts 的 recoverDeadTeamIdentityOnce）。
 */
export async function recoverToPersonalIdentity(): Promise<boolean> {
  try {
    const { token } = await switchContext(0);
    applyIdentityToken(token);
    return true;
  } catch {
    clearAuthSession();
    if (typeof window !== 'undefined' && !window.location.pathname.startsWith('/login')) {
      window.location.href = '/login';
    }
    return false;
  }
}
