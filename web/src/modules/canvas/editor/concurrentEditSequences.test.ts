import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { beginInteraction, endInteraction, resetInteractionRegistry } from './interactionRegistry';
import { commitTextDraft, flushTextDraft } from './nodes/text/textNodeDraft';
import {
  configureCanvasPersistence,
  flushCanvasMutations,
  resetCanvasPersistenceForTest,
  seedCanvasBaselines,
  setCanvasOwnerToken,
} from './persistence/canvasMutations';
import { clearDeferredRemoteOps, useCanvasStore } from './store';
import type { CanvasNode } from './types';

// **真实事件序**的集成测试。与 store.remoteOp.test.ts 的区别是：那边手工注入
// pendingLocalPatchProvider 来验证 strip 逻辑本身，这里一行都不注入——走的是
// 组件真正会走的那串调用（commitTextDraft → flushTextDraft → endInteraction），
// 保护窗口有没有覆盖真实时序，只有这种测试问得出来。
//
// 被这两条覆盖的两个真实缺陷：
//   C1 文本失焦：flushNodePatch **同步**把 patch 从 pending 队列摘走（await 之前），
//      而 endInteraction 要等 React 提交后的 effect cleanup 才跑——只看 pending
//      的保护在这一小段里恰好是空的，队友的旧文本整段覆盖刚打的字。
//   I2 在飞窗口：交互早已结束、请求还在飞时，旧序远端帧直接落地覆盖已提交字段。

function textNode(id: string, text: string): CanvasNode {
  return {
    id,
    type: 'text-node',
    x: 0,
    y: 0,
    width: 498,
    height: 280,
    settings: { text },
    status: 'idle',
  };
}

// 让入队的 mutation 真正跑起来（队列是 promise 链，请求在下一批微任务里才发出）。
async function letRequestFly() {
  for (let i = 0; i < 6; i += 1) await Promise.resolve();
}

function currentText(id: string): unknown {
  return useCanvasStore.getState().nodes.find((n) => n.id === id)?.settings?.text;
}

describe('文本节点失焦 → 挂起远端帧落地（真实时序）', () => {
  let releaseRequest: () => void;
  let patchNode: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    resetCanvasPersistenceForTest();
    resetInteractionRegistry();
    clearDeferredRemoteOps();
    useCanvasStore.setState({
      nodes: [textNode('t1', 'server text')],
      connections: [],
      selectedIds: new Set<string>(),
      undoStack: [],
      redoStack: [],
      readOnly: false,
    });
    // 请求发出后挂住不 settle，模拟真实的在飞窗口。
    patchNode = vi.fn(() => new Promise<{ version: number }>((resolve) => {
      releaseRequest = () => resolve({ version: 9 });
    }));
    configureCanvasPersistence({ projectID: 42, patchNode });
    setCanvasOwnerToken('owner-token');
    seedCanvasBaselines([textNode('t1', 'server text')]);
  });

  afterEach(() => {
    resetCanvasPersistenceForTest();
    resetInteractionRegistry();
  });

  it('blur → flushNodePatch → effect cleanup 全序下，我刚打的字保住', async () => {
    // 1) 进入编辑（useNodeEditingBroadcast 的 effect）。
    beginInteraction('t1', 'text');
    // 2) 打字（Tiptap onUpdate → commitTextDraft：改内存 + 入 debounce 队列）。
    commitTextDraft('t1', 'local typed');

    // 3) 队友在我编辑期间改了同一段文字 → 交互中，挂起。
    useCanvasStore.getState().applyRemoteOp({
      kind: 'patchNode',
      nodeId: 't1',
      patch: { settings: { text: 'peer text' } },
    });
    expect(currentText('t1')).toBe('local typed');

    // 4) 失焦：onBlur 里 `void flushTextDraft(...)`（**不等**它 settle）→ onExit。
    void flushTextDraft('t1');
    // 5) React 提交后 effect cleanup 才跑 endInteraction → flush 挂起帧。
    endInteraction('t1', 'text');

    // 我打的字必须还在（旧实现在这里回退成 'peer text'，且没有任何帧会纠正）。
    expect(currentText('t1')).toBe('local typed');

    await letRequestFly();
    releaseRequest();
    await flushCanvasMutations();
    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 't1', {
      settings: { text: 'local typed' },
    });
  });

  it('请求 settle 之后保护窗关闭：后到的远端改动照常落地', async () => {
    beginInteraction('t1', 'text');
    commitTextDraft('t1', 'local typed');
    void flushTextDraft('t1');
    endInteraction('t1', 'text');

    await letRequestFly();
    releaseRequest();
    await flushCanvasMutations();

    useCanvasStore.getState().applyRemoteOp({
      kind: 'patchNode',
      nodeId: 't1',
      patch: { settings: { text: 'peer later' } },
    });
    expect(currentText('t1')).toBe('peer later');
  });

  it('在飞窗口（交互已结束）内的远端帧同样盖不掉已提交字段', async () => {
    // 没有任何交互登记：纯粹是「请求在飞、旧序帧后到」的时序（审查 I2）。
    commitTextDraft('t1', 'local typed');
    void flushTextDraft('t1');

    useCanvasStore.getState().applyRemoteOp({
      kind: 'patchNode',
      nodeId: 't1',
      patch: { settings: { text: 'stale peer' } },
    });
    expect(currentText('t1')).toBe('local typed');

    await letRequestFly();
    releaseRequest();
    await flushCanvasMutations();
  });
});
