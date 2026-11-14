import { create } from 'zustand';
import { isCanvasPollStalled } from '@/shared/api';

// 轮询挂起标记(pollStalled,P23)。
//
// pollCanvasTaskUntilDone 连续多次够不着后端时以 CanvasPollStalledError 结束:任务状态
// 未知,节点保持 generating,只在这里记下「这个任务的轮询挂起了」。节点的轮询 effect 见到
// 标记就不再自动重启,改由 useReconcileCanvasTask 低频对账;NodeShell 显示「连接中断,
// 点击重新检查」,点了就清标记、让轮询 effect 立刻重来一轮。
//
// 按 taskId 记、放在独立的小 store 里而不是节点字段:它是这一页的连接状态,不该落库、
// 进撤销栈或跟着复制走;按 taskId 记,画布和故事板详情里同一节点的两份组件看到同一个值。
interface PollStallState {
  stalled: ReadonlySet<number>;
}

export const usePollStallStore = create<PollStallState>(() => ({ stalled: new Set<number>() }));

export function usePollStalled(taskId: number | undefined): boolean {
  return usePollStallStore((s) => (taskId ? s.stalled.has(taskId) : false));
}

export function markPollStalled(taskId: number): void {
  const cur = usePollStallStore.getState().stalled;
  if (cur.has(taskId)) return;
  const next = new Set(cur);
  next.add(taskId);
  usePollStallStore.setState({ stalled: next });
}

export function clearPollStalled(taskId: number): void {
  const cur = usePollStallStore.getState().stalled;
  if (!cur.has(taskId)) return;
  const next = new Set(cur);
  next.delete(taskId);
  usePollStallStore.setState({ stalled: next });
}

/**
 * 节点轮询 .catch 里先过这一道:是「连接中断」就只挂起、返回 true,调用方不要判失败、
 * 也不要 persist(那会把后端刚写回快照的结果覆盖掉)。其余错误(含连续 404 的
 * CanvasTaskGoneError)返回 false,照旧判失败。
 */
export function stallIfUnreachable(taskId: number, err: unknown): boolean {
  if (!isCanvasPollStalled(err)) return false;
  markPollStalled(taskId);
  return true;
}
