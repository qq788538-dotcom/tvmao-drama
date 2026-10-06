import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useCanvasStore } from '../store';
import type { CanvasNode } from '../types';
import {
  configureCanvasPersistence,
  flushCanvasMutations,
  persistNodeById,
  resetCanvasPersistenceForTest,
  scheduleNodePatch,
  seedCanvasBaselines,
  setCanvasOwnerToken,
} from './canvasMutations';

// persistNodeById 的字段级 diff 行为。
//
// 旧实现每次都整节点 upsert——并发编辑下等于「我这份节点状态覆盖服务端全部字段」，
// 队友刚改的字段会被我毫不知情地写回旧值。新实现拿 per-node 服务端基线做 diff，
// 只发变更字段走 PatchNode（服务端递归合并），把 LWW 收敛到字段级。

function node(id: string, patch: Partial<CanvasNode> = {}): CanvasNode {
  return {
    id,
    type: 'gen-image',
    x: 0,
    y: 0,
    width: 280,
    height: 280,
    settings: {},
    status: 'idle',
    ...patch,
  };
}

function setNodes(nodes: CanvasNode[]) {
  useCanvasStore.setState({ nodes });
}

describe('persistNodeById 字段级 diff', () => {
  let patchNode: ReturnType<typeof vi.fn>;
  let upsertNode: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.useFakeTimers();
    resetCanvasPersistenceForTest();
    setNodes([]);
    patchNode = vi.fn(async () => ({ version: 1 }));
    upsertNode = vi.fn(async () => ({ version: 1 }));
    configureCanvasPersistence({ projectID: 42, patchNode, upsertNode });
    setCanvasOwnerToken('owner-token');
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('没有基线（新节点）时发整节点 upsert', async () => {
    setNodes([node('n1', { content: 'https://x/a.png' })]);

    persistNodeById('n1');
    await flushCanvasMutations();

    expect(upsertNode).toHaveBeenCalledTimes(1);
    expect(upsertNode).toHaveBeenCalledWith(42, 'owner-token', expect.objectContaining({ id: 'n1' }));
    expect(patchNode).not.toHaveBeenCalled();
  });

  it('upsert 成功即建立基线，之后的改动走 patch', async () => {
    setNodes([node('n1')]);
    persistNodeById('n1');
    await flushCanvasMutations();

    setNodes([node('n1', { content: 'https://x/a.png' })]);
    persistNodeById('n1');
    await flushCanvasMutations();

    expect(upsertNode).toHaveBeenCalledTimes(1);
    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'n1', { content: 'https://x/a.png' });
  });

  it('有基线时只发变更字段，递归到叶子标量', async () => {
    seedCanvasBaselines([
      node('n1', { settings: { prompt: 'old', params: { seed: 1, ratio: '1:1' } } }),
    ]);
    setNodes([node('n1', { settings: { prompt: 'old', params: { seed: 2, ratio: '1:1' } } })]);

    persistNodeById('n1');
    await flushCanvasMutations();

    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'n1', {
      settings: { params: { seed: 2 } },
    });
    expect(upsertNode).not.toHaveBeenCalled();
  });

  it('数组整体比较、整体发送（与后端数组整体替换对称）', async () => {
    seedCanvasBaselines([node('n1', { settings: { history: [{ url: 'a' }, { url: 'b' }] } })]);
    setNodes([node('n1', { settings: { history: [{ url: 'a' }, { url: 'c' }] } })]);

    persistNodeById('n1');
    await flushCanvasMutations();

    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'n1', {
      settings: { history: [{ url: 'a' }, { url: 'c' }] },
    });
  });

  it('与基线完全一致时一个请求都不发', async () => {
    seedCanvasBaselines([node('n1', { settings: { prompt: 'same', history: ['h'] } })]);
    setNodes([node('n1', { settings: { prompt: 'same', history: ['h'] } })]);

    persistNodeById('n1');
    await flushCanvasMutations();

    expect(patchNode).not.toHaveBeenCalled();
    expect(upsertNode).not.toHaveBeenCalled();
  });

  // 生成路径每跑完一次就要清掉 taskId / settings.pendingGen 这类阶段字段——
  // 这条是最热的写入路径，退回整节点 upsert 等于字段级 LWW 在这里失效。
  it('字段被删除时发 null（RFC7386 删键），不退回整节点 upsert', async () => {
    seedCanvasBaselines([node('n1', { taskId: 7, settings: { prompt: 'p', pendingGen: { at: 1 } } })]);
    setNodes([node('n1', { settings: { prompt: 'p' } })]);

    persistNodeById('n1');
    await flushCanvasMutations();

    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'n1', {
      taskId: null,
      settings: { pendingGen: null },
    });
    expect(upsertNode).not.toHaveBeenCalled();
  });

  it('patch 不带 id（节点 id 是 URL 上的定位参数，不是待改字段）', async () => {
    seedCanvasBaselines([node('n1')]);
    setNodes([node('n1', { content: 'c' })]);

    persistNodeById('n1');
    await flushCanvasMutations();

    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'n1', { content: 'c' });
  });

  // H1：基线只能记服务端做过的那一步。若把「远端 patch 合并后的 store 节点」
  // 当基线，本地那些还没发出去的编辑会被算成「无变更」，永远发不出且无报错。
  it('本地有未发出的编辑时，远端 patch 落地不会吞掉它', async () => {
    seedCanvasBaselines([node('n1', { settings: { prompt: 'server' } })]);
    // 本地已改 prompt，但还没触发持久化。
    setNodes([node('n1', { settings: { prompt: 'local' } })]);

    // 队友改了另一个字段 → 远端 patch 合并进 store。
    useCanvasStore.getState().applyRemoteOp({
      kind: 'patchNode',
      nodeId: 'n1',
      patch: { settings: { ratio: '16:9' } },
    });

    persistNodeById('n1');
    await flushCanvasMutations();

    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'n1', {
      settings: { prompt: 'local' },
    });
  });

  it('值为 undefined 等同于「没有这个字段」，不当成变更', async () => {
    seedCanvasBaselines([node('n1', { settings: { prompt: 'p' } })]);
    setNodes([node('n1', { settings: { prompt: 'p' }, progress: undefined })]);

    persistNodeById('n1');
    await flushCanvasMutations();

    expect(patchNode).not.toHaveBeenCalled();
    expect(upsertNode).not.toHaveBeenCalled();
  });

  it('远端 op 落地后基线跟着推进——本地 diff 不会把队友的字段改回去', async () => {
    seedCanvasBaselines([node('n1', { settings: { prompt: 'p' } })]);
    setNodes([node('n1', { settings: { prompt: 'p' } })]);

    // 队友改了 x（远端 op 已合并进 store，同时推进基线）。
    useCanvasStore.getState().applyRemoteOp({ kind: 'patchNode', nodeId: 'n1', patch: { x: 500 } });
    // 本地随后改了 content。
    setNodes([node('n1', { x: 500, content: 'local', settings: { prompt: 'p' } })]);

    persistNodeById('n1');
    await flushCanvasMutations();

    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'n1', { content: 'local' });
  });

  it('远端删除后基线一并清除，同 id 节点重新出现时走 upsert', async () => {
    seedCanvasBaselines([node('n1')]);
    setNodes([node('n1')]);
    useCanvasStore.getState().applyRemoteOp({ kind: 'deleteNodes', nodeIds: ['n1'] });

    setNodes([node('n1', { content: 'back' })]);
    persistNodeById('n1');
    await flushCanvasMutations();

    expect(upsertNode).toHaveBeenCalledTimes(1);
    expect(patchNode).not.toHaveBeenCalled();
  });

  it('scheduleNodePatch 落库成功后也推进基线，避免重复发同一字段', async () => {
    seedCanvasBaselines([node('n1', { settings: { prompt: 'old' } })]);
    setNodes([node('n1', { settings: { prompt: 'new' } })]);

    scheduleNodePatch('n1', { settings: { prompt: 'new' } });
    await vi.advanceTimersByTimeAsync(1500);
    await flushCanvasMutations();
    expect(patchNode).toHaveBeenCalledTimes(1);

    persistNodeById('n1');
    await flushCanvasMutations();

    expect(patchNode).toHaveBeenCalledTimes(1);
  });

  it('store 里没有这个节点时什么都不发', async () => {
    setNodes([]);
    persistNodeById('ghost');
    await flushCanvasMutations();

    expect(patchNode).not.toHaveBeenCalled();
    expect(upsertNode).not.toHaveBeenCalled();
  });

  it('没有 ownerToken 时静默丢弃（等同只读）', async () => {
    setCanvasOwnerToken('');
    setNodes([node('n1')]);

    persistNodeById('n1');
    await flushCanvasMutations();

    expect(upsertNode).not.toHaveBeenCalled();
  });

  it('切项目后旧基线不残留：新项目的同 id 节点走 upsert', async () => {
    seedCanvasBaselines([node('n1', { settings: { prompt: 'p' } })]);
    configureCanvasPersistence({ projectID: 43, patchNode, upsertNode });
    setCanvasOwnerToken('owner-token-43');
    setNodes([node('n1', { settings: { prompt: 'q' } })]);

    persistNodeById('n1');
    await flushCanvasMutations();

    expect(upsertNode).toHaveBeenCalledTimes(1);
    expect(patchNode).not.toHaveBeenCalled();
  });
});
