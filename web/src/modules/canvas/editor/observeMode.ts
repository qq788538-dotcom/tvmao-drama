// 团队观看模式的两条纯判定规则。抽出来单独放是因为它们各自都有一个
// 「反过来写就静默出错」的边界（未知 stale、两段式 token），单测钉得住，
// 混在页面组件里则只能靠端到端才发现。
import type { CanvasOwnerProbe } from '@/shared/api';

/**
 * 持锁者被判定为「已经离开」的锁龄阈值（秒）。
 *
 * 心跳周期是 10s，锁 TTL 是 24h 且关页面**不释放**——只看 probe 的 `held`
 * 会把全团队顶在只读里一整天（有人早上开了一下就关了浏览器）。60s 是留了
 * 5 个心跳周期的余量：短暂卡顿/切后台不至于被误判成离开，真关了页面则最多
 * 一分钟后队友就能正常编辑。
 */
export const HOLDER_STALE_LIMIT_SECONDS = 60;

/**
 * 该不该进观看模式（不 Claim、只读旁观）。
 *
 * 三个条件缺一不可：锁被持有、持有者不是自己（自己另一个 tab 照旧 steal，
 * 现有语义不动）、且持有者还活着。
 *
 * `staleSeconds === -1`（未知）保守算「还活着」→ 进观看：宁可让人多点一次
 * 「接管编辑」，也不要在探测失灵时默默把正在编辑的同事踢下线。这里
 * `-1 <= 60` 天然成立，不需要额外分支——但**不能**把它当 0 或极大值理解。
 */
export function shouldObserve(probe: CanvasOwnerProbe | null | undefined): boolean {
  if (!probe || !probe.held || probe.isSelf) return false;
  return probe.staleSeconds <= HOLDER_STALE_LIMIT_SECONDS;
}

/**
 * 从本页 ownerToken 算出与后端同规则的 8 位来源指纹，用来跳过自己触发的
 * 变更回声（后端 canvasowner.SourceMark 是唯一真源）。
 *
 * 取的是**末段**（随机 hex）的前 8 位，不是整串的前 8 位——锁值热升级期间
 * 两段式 `<userID>:<hex>` 与三段式 `<userID>:<actorID>:<hex>` 同时存在，
 * 按整串截前 8 位会把 userID 也截进去，跟服务端算出来的对不上，于是自己的
 * 每一次改动都会让自己回拉一次快照。
 *
 * 观看者没有 token → 返回空串 → 调用方视为「恒刷新」（观看端收到的每一帧
 * 都来自别人）。
 */
export function ownerTokenMark(token: string | null | undefined): string {
  if (!token) return '';
  return (token.split(':').pop() ?? '').slice(0, 8);
}
