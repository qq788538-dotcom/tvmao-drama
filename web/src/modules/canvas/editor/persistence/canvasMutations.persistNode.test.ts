import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/shared/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/shared/api')>()),
  patchCanvasNodePositions: vi.fn(async () => ({ version: 1 })),
}));

import { patchCanvasNodePositions } from '@/shared/api';
import { useCanvasStore } from '../store';
import type { CanvasNode } from '../types';
import {
  configureCanvasPersistence,
  flushCanvasMutations,
  flushNodePatch,
  persistNodeById,
  persistNodePositions,
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

  it('值为 undefined 等同于「没有这个字段」，不当成变更', async () => {
    seedCanvasBaselines([node('n1', { settings: { prompt: 'p' } })]);
    setNodes([node('n1', { settings: { prompt: 'p' }, progress: undefined })]);

    persistNodeById('n1');
    await flushCanvasMutations();

    expect(patchNode).not.toHaveBeenCalled();
    expect(upsertNode).not.toHaveBeenCalled();
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

  // 三条保存路径（拖拽落库 / 文本失焦保存 / 生成回填）在删除协作在途登记前后行为不变的守护。
  it('拖拽落库：persistNodePositions 发几何 PATCH，成功后推进基线，随后 persistNodeById 不重发 x/y', async () => {
    vi.mocked(patchCanvasNodePositions).mockClear();
    seedCanvasBaselines([node('n1')]);
    setNodes([node('n1', { x: 40, y: 30 })]);

    persistNodePositions(42, 'owner-token', [{ id: 'n1', x: 40, y: 30, width: 280, height: 280 }]);
    await flushCanvasMutations();
    expect(patchCanvasNodePositions).toHaveBeenCalledTimes(1);
    expect(vi.mocked(patchCanvasNodePositions).mock.calls[0]).toEqual([
      42,
      'owner-token',
      [{ id: 'n1', x: 40, y: 30, width: 280, height: 280 }],
    ]);

    persistNodeById('n1');
    await flushCanvasMutations();
    expect(patchNode).not.toHaveBeenCalled();
    expect(upsertNode).not.toHaveBeenCalled();
  });

  it('文本失焦保存：flushNodePatch 不等 debounce 立即发 patch，成功后推进基线', async () => {
    seedCanvasBaselines([node('t1', { type: 'text-node', settings: { text: 'old' } })]);
    setNodes([node('t1', { type: 'text-node', settings: { text: 'typed' } })]);

    scheduleNodePatch('t1', { settings: { text: 'typed' } });
    await flushNodePatch('t1');
    expect(patchNode).toHaveBeenCalledTimes(1);
    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 't1', { settings: { text: 'typed' } });

    // debounce 到点也不会再发第二次；基线已推进，persistNodeById 无差量。
    await vi.advanceTimersByTimeAsync(1500);
    persistNodeById('t1');
    await flushCanvasMutations();
    expect(patchNode).toHaveBeenCalledTimes(1);
  });

  it('生成回填：只发结果与状态字段，阶段字段发 null，成功后基线推进', async () => {
    seedCanvasBaselines([
      node('g1', { status: 'generating', taskId: 9, settings: { prompt: 'p', pendingGen: { at: 1 } } }),
    ]);
    setNodes([node('g1', { status: 'succeeded', content: 'https://x/r.png', settings: { prompt: 'p' } })]);

    persistNodeById('g1');
    await flushCanvasMutations();
    expect(patchNode).toHaveBeenCalledTimes(1);
    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'g1', {
      status: 'succeeded',
      content: 'https://x/r.png',
      taskId: null,
      settings: { pendingGen: null },
    });

    persistNodeById('g1');
    await flushCanvasMutations();
    expect(patchNode).toHaveBeenCalledTimes(1);
  });

  it('patch 失败：交给 onError，基线不推进，下次 persistNodeById 重发同一差量', async () => {
    const onError = vi.fn();
    patchNode.mockRejectedValueOnce(new Error('boom'));
    configureCanvasPersistence({ projectID: 42, patchNode, upsertNode, onError });
    seedCanvasBaselines([node('n1')]);
    setNodes([node('n1', { content: 'c' })]);

    persistNodeById('n1');
    await flushCanvasMutations();
    expect(onError).toHaveBeenCalledTimes(1);

    persistNodeById('n1');
    await flushCanvasMutations();
    expect(patchNode).toHaveBeenCalledTimes(2);
    expect(patchNode).toHaveBeenLastCalledWith(42, 'owner-token', 'n1', { content: 'c' });
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

// P8：抽帧 dataURL 与超大 dataURL 不进服务端 payload；store 里的节点保持原样。
describe('发往服务端的节点净化', () => {
  let patchNode: ReturnType<typeof vi.fn>;
  let upsertNode: ReturnType<typeof vi.fn>;
  const frame = { url: 'data:image/jpeg;base64,AAAA', time: 0 };
  const bigDataUrl = `data:image/png;base64,${'A'.repeat(64 * 1024)}`;

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

  function videoNode(settings: Record<string, unknown>): CanvasNode {
    return node('v1', { type: 'video-input', content: 'https://x/v.mp4', settings });
  }

  it('video-input 整节点 upsert 不带 frames/selectedKeyframes/extractingFrames，store 不受影响', async () => {
    setNodes([videoNode({ frames: [frame], selectedKeyframes: [frame], extractingFrames: false, videoExpanded: true })]);

    persistNodeById('v1');
    await flushCanvasMutations();

    const sent = upsertNode.mock.calls[0][2] as CanvasNode;
    expect(sent.settings).toEqual({ videoExpanded: true });
    expect(JSON.stringify(sent)).not.toContain('data:image');
    expect(useCanvasStore.getState().nodes[0].settings?.frames).toEqual([frame]);
  });

  it('有基线时抽帧变化不算差量：只抽帧不发请求', async () => {
    seedCanvasBaselines([videoNode({ videoExpanded: true })]);
    setNodes([videoNode({ videoExpanded: true, frames: [frame], selectedKeyframes: [frame] })]);

    persistNodeById('v1');
    await flushCanvasMutations();

    expect(patchNode).not.toHaveBeenCalled();
    expect(upsertNode).not.toHaveBeenCalled();
  });

  it('存量快照里带着帧：下一次落库对这些键发 null，把服务端那份删掉', async () => {
    seedCanvasBaselines([videoNode({ frames: [frame], selectedKeyframes: [frame] })]);
    setNodes([videoNode({ frames: [frame], selectedKeyframes: [frame], videoMeta: { duration: 3 } })]);

    persistNodeById('v1');
    await flushCanvasMutations();

    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'v1', {
      settings: { frames: null, selectedKeyframes: null, videoMeta: { duration: 3 } },
    });
  });

  it('其他节点同名字段不受影响（只剥 video-input）', async () => {
    setNodes([node('n1', { settings: { frames: [1, 2] } })]);

    persistNodeById('n1');
    await flushCanvasMutations();

    expect((upsertNode.mock.calls[0][2] as CanvasNode).settings).toEqual({ frames: [1, 2] });
  });

  it('超过 64KB 的 data: 字符串一律不进 upsert，小 dataURL 与普通长串照常', async () => {
    const longText = 'x'.repeat(64 * 1024 + 1);
    setNodes([node('n1', {
      content: bigDataUrl,
      settings: { icon: 'data:image/png;base64,AA', text: longText, list: ['a', bigDataUrl] },
    })]);

    persistNodeById('n1');
    await flushCanvasMutations();

    const sent = upsertNode.mock.calls[0][2] as CanvasNode;
    expect(sent.content).toBeUndefined();
    expect(sent.settings).toEqual({ icon: 'data:image/png;base64,AA', text: longText, list: ['a', null] });
  });

  it('scheduleNodePatch 的 payload 同样拒收超大 dataURL', async () => {
    seedCanvasBaselines([node('n1')]);
    setNodes([node('n1')]);

    scheduleNodePatch('n1', { settings: { prompt: 'p', thumb: bigDataUrl } });
    await flushNodePatch('n1');

    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'n1', { settings: { prompt: 'p' } });
  });
});
