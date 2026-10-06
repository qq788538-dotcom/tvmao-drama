// 「本地正在交互的节点」登记簿。
//
// 为什么需要它：团队画布并发编辑下，远端 op 随时会落到任意节点上。如果队友的
// patch 在我正拖着这个节点 / 正在这个文本节点里打字的时候直接合并进 store，
// 用户会看到节点在手底下跳位、光标处的文字被换掉——本地在途的操作还没提交，
// 就被远端状态覆盖了。所以远端 op 对「交互中」的节点必须先挂起（见 store.ts
// 的 deferredRemoteOps），交互结束再落地。
//
// 为什么单独成模块而不是塞进 store：它不是画布数据的一部分（不进快照、不进
// undo 栈、不触发渲染订阅），而且 T4 的「xx 正在编辑」角标也要读它。store.ts
// 单向 import 本模块，本模块不 import store——交互结束的通知走 listener，
// 避免两个模块循环依赖。
export type InteractionKind = 'drag' | 'text';

// nodeId → 当前正在进行的交互种类集合。同一节点可能同时被多种交互占用
// （比如文本节点聚焦编辑时被一起框选拖动），所以是集合而不是单值：
// 必须等最后一种交互也结束才算脱离在途状态。
const interactions = new Map<string, Set<InteractionKind>>();
const endListeners = new Set<(nodeId: string) => void>();

export function beginInteraction(nodeId: string, kind: InteractionKind): void {
  const kinds = interactions.get(nodeId);
  if (kinds) {
    kinds.add(kind);
    return;
  }
  interactions.set(nodeId, new Set([kind]));
}

// 结束一种交互。该节点上所有交互都结束后才广播 end——挂起的远端 op 这时才
// 允许落地。重复调用（松手事件与 blur 事件各调一次的常见形态）是幂等的。
export function endInteraction(nodeId: string, kind: InteractionKind): void {
  const kinds = interactions.get(nodeId);
  if (!kinds) return;
  kinds.delete(kind);
  if (kinds.size > 0) return;
  interactions.delete(nodeId);
  for (const listener of endListeners) {
    try {
      listener(nodeId);
    } catch {
      // 单个监听者出错不能挡住其余监听者（同 canvasMutations 的 owner-lost 广播）。
    }
  }
}

export function isNodeInteracting(nodeId: string): boolean {
  return interactions.has(nodeId);
}

export function onInteractionEnd(listener: (nodeId: string) => void): () => void {
  endListeners.add(listener);
  return () => {
    endListeners.delete(listener);
  };
}

// 离开画布 / 切换项目时清场。**不**广播 end：广播会让挂起的过期远端 op 落到
// 新画布上。挂起队列由 store 自己清（`clearDeferredRemoteOps`，`load()` 已经会
// 调一次），本模块只管交互登记这一半——调用方两个都要调。
export function resetInteractionRegistry(): void {
  interactions.clear();
}
