import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  TRANSIENT_POSITION_TTL_MS,
  clearDeferredRemoteOps,
  clearTransientPositions,
  resolveNodePosition,
  setCanvasOpSink,
  setRemoteNodeStateSink,
  useCanvasStore,
  type CanvasOp,
  type RemoteNodeStateOp,
} from './store';
import {
  configureCanvasPersistence,
  persistNodeById,
  resetCanvasPersistenceForTest,
  seedCanvasBaselines,
  setCanvasOwnerToken,
} from './persistence/canvasMutations';
import { beginInteraction, endInteraction, resetInteractionRegistry } from './interactionRegistry';
import type { CanvasNode } from './types';

// applyTransientPositions：队友拖拽途中的实时坐标。
// 核心不变量：**只进覆盖层，永不进 nodes[]**——nodes[] 是 persistNodeById 的
// diff 面，瞬态一旦写进去就会被本端当成自己的改动写回服务端。

function node(id: string, x = 0, y = 0): CanvasNode {
  return { id, type: 'gen-image', x, y, width: 280, height: 280, settings: {}, status: 'idle' };
}

function nodeById(id: string): CanvasNode | undefined {
  return useCanvasStore.getState().nodes.find((n) => n.id === id);
}

function overlayOf(id: string) {
  return useCanvasStore.getState().transientPositions[id];
}

function seedStore() {
  useCanvasStore.setState({
    nodes: [node('n1'), node('n2', 100, 100)],
    connections: [],
    transientPositions: {},
    view: { x: 0, y: 0, zoom: 1 },
    selectedIds: new Set<string>(),
    undoStack: [],
    redoStack: [],
    readOnly: false,
  });
  clearDeferredRemoteOps();
  clearTransientPositions();
  resetInteractionRegistry();
}

describe('applyTransientPositions', () => {
  let ops: CanvasOp[];
  let baselineWrites: Array<[string, RemoteNodeStateOp | null]>;

  beforeEach(() => {
    vi.useFakeTimers();
    seedStore();
    ops = [];
    baselineWrites = [];
    setCanvasOpSink((op) => ops.push(op));
    setRemoteNodeStateSink((id, op) => baselineWrites.push([id, op]));
  });

  afterEach(() => {
    clearTransientPositions();
    setCanvasOpSink(null);
    setRemoteNodeStateSink(null);
    vi.useRealTimers();
  });

  it('只写覆盖层，nodes[] 的坐标一动不动', () => {
    useCanvasStore.getState().applyTransientPositions([
      { id: 'n1', x: 30, y: 40 },
      { id: 'n2', x: 50, y: 60 },
    ]);

    expect(overlayOf('n1')).toMatchObject({ x: 30, y: 40 });
    expect(overlayOf('n2')).toMatchObject({ x: 50, y: 60 });
    expect(nodeById('n1')).toMatchObject({ x: 0, y: 0 });
    expect(nodeById('n2')).toMatchObject({ x: 100, y: 100 });
  });

  it('渲染层按覆盖层取位置，没有条目时用节点自身坐标', () => {
    useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: 30, y: 40 }]);
    expect(resolveNodePosition(nodeById('n1')!, overlayOf('n1'))).toEqual({ x: 30, y: 40 });
    expect(resolveNodePosition(nodeById('n2')!, overlayOf('n2'))).toEqual({ x: 100, y: 100 });
  });

  // 【H1】这条是本层存在的**唯一理由**：瞬态写进 nodes[] 的话，本端之后任何一次
  // 持久化（改个提示词都算）都会把队友拖到一半的坐标当成自己的改动 PATCH 回去，
  // 覆盖掉队友松手后的真实位置，且不会被任何广播纠正。
  it('瞬态坐标结构性够不到持久化 diff：本地 persist 不带队友的中间坐标', async () => {
    const patchNode = vi.fn(
      async (_projectID: number, _token: string, _nodeID: string, _patch: Record<string, unknown>) => ({ version: 2 }),
    );
    resetCanvasPersistenceForTest();
    configureCanvasPersistence({ projectID: 42, patchNode });
    setCanvasOwnerToken('owner-token');
    seedCanvasBaselines([node('n1')]);

    // 队友把 n1 拖到 (300,400)，本端只改了提示词。
    useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: 300, y: 400 }]);
    useCanvasStore.getState().updateNodeSettings('n1', { prompt: '我改的' });
    persistNodeById('n1');
    await vi.advanceTimersByTimeAsync(0);

    expect(patchNode).toHaveBeenCalledTimes(1);
    const patch = patchNode.mock.calls[0][3];
    expect(patch).toMatchObject({ settings: { prompt: '我改的' } });
    expect(patch).not.toHaveProperty('x');
    expect(patch).not.toHaveProperty('y');
    resetCanvasPersistenceForTest();
  });

  it('不进 undo 栈、不触发持久化 sink、不回吐基线', () => {
    useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: 30, y: 40 }]);
    expect(useCanvasStore.getState().undoStack).toHaveLength(0);
    expect(ops).toHaveLength(0);
    expect(baselineWrites).toHaveLength(0);
  });

  it('本地正在拖拽的节点忽略远端瞬态，其余节点照常落地', () => {
    beginInteraction('n1', 'drag');
    useCanvasStore.getState().applyTransientPositions([
      { id: 'n1', x: 30, y: 40 },
      { id: 'n2', x: 50, y: 60 },
    ]);
    expect(overlayOf('n1')).toBeUndefined();
    expect(overlayOf('n2')).toBeDefined();
  });

  it('被忽略的瞬态不会在交互结束后补落地', () => {
    beginInteraction('n1', 'drag');
    useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: 30, y: 40 }]);
    endInteraction('n1', 'drag');
    expect(overlayOf('n1')).toBeUndefined();
  });

  it('坏条目静默跳过', () => {
    useCanvasStore.getState().applyTransientPositions([
      { id: 'n1', x: Number('a'), y: 6 } as never,
      { id: 'n2', x: 5, y: 6 },
    ]);
    expect(overlayOf('n2')).toMatchObject({ x: 5, y: 6 });
  });

  // 流断了（对方掉线/切走）必须回落到持久化坐标，不能让节点永远停在最后一帧的
  // 中间位置——那是「看起来数据错了」，刷新才好。
  it('覆盖条目 TTL 到点自动过期，位置回落到 node 自身坐标', async () => {
    useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: 30, y: 40 }]);
    await vi.advanceTimersByTimeAsync(TRANSIENT_POSITION_TTL_MS - 100);
    expect(overlayOf('n1')).toBeDefined();

    await vi.advanceTimersByTimeAsync(700);
    expect(overlayOf('n1')).toBeUndefined();
    expect(resolveNodePosition(nodeById('n1')!, overlayOf('n1'))).toEqual({ x: 0, y: 0 });
  });

  it('持续续帧不会被 TTL 打断', async () => {
    for (let i = 0; i < 6; i += 1) {
      useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: i, y: i }]);
      await vi.advanceTimersByTimeAsync(400);
    }
    expect(overlayOf('n1')).toMatchObject({ x: 5, y: 5 });
  });

  it('load（整拉/切项目）清空覆盖层', () => {
    useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: 30, y: 40 }]);
    useCanvasStore.getState().load({ nodes: [node('n1')], connections: [] });
    expect(useCanvasStore.getState().transientPositions).toEqual({});
  });
});

// 【M1】权威几何必须压过瞬态：队友松手后的 patchPositions 是最终位置，此后到达
// 的旧瞬态帧属于反序噪声——落下去就把节点又拖回中间态，而且没有下一帧来纠正
// （对方已松手，不再发瞬态）。
describe('权威几何 op 与瞬态的先后', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    seedStore();
    setCanvasOpSink(null);
    setRemoteNodeStateSink(null);
  });
  afterEach(() => {
    clearTransientPositions();
    vi.useRealTimers();
  });

  it('patchPositions 清覆盖层，并在抑制窗内丢弃后到的瞬态', () => {
    useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: 30, y: 40 }]);
    useCanvasStore.getState().applyRemoteOp({
      kind: 'patchPositions',
      positions: [{ id: 'n1', x: 500, y: 600 }],
    });

    expect(overlayOf('n1')).toBeUndefined();
    expect(nodeById('n1')).toMatchObject({ x: 500, y: 600 });

    // 反序到达的旧瞬态：抑制窗内不许盖住定格位置。
    useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: 33, y: 44 }]);
    expect(overlayOf('n1')).toBeUndefined();
    expect(resolveNodePosition(nodeById('n1')!, overlayOf('n1'))).toEqual({ x: 500, y: 600 });
  });

  it('抑制窗过后（队友重新拖起来）瞬态照常生效', () => {
    useCanvasStore.getState().applyRemoteOp({
      kind: 'patchPositions',
      positions: [{ id: 'n1', x: 500, y: 600 }],
    });
    vi.advanceTimersByTime(500);
    useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: 520, y: 610 }]);
    expect(overlayOf('n1')).toMatchObject({ x: 520, y: 610 });
  });

  it('upsertNode（整节点带坐标）同样定格并抑制', () => {
    useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: 30, y: 40 }]);
    useCanvasStore.getState().applyRemoteOp({ kind: 'upsertNode', node: node('n1', 700, 800) });

    expect(overlayOf('n1')).toBeUndefined();
    useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: 31, y: 41 }]);
    expect(overlayOf('n1')).toBeUndefined();
  });

  it('带 x/y 的 patchNode 定格；不带几何的 patchNode 不打断拖拽流', () => {
    useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: 30, y: 40 }]);
    // 改提示词不该把队友正在进行的拖拽流打断。
    useCanvasStore.getState().applyRemoteOp({
      kind: 'patchNode', nodeId: 'n1', patch: { settings: { prompt: 'x' } },
    });
    expect(overlayOf('n1')).toMatchObject({ x: 30, y: 40 });

    useCanvasStore.getState().applyRemoteOp({ kind: 'patchNode', nodeId: 'n1', patch: { x: 900 } });
    expect(overlayOf('n1')).toBeUndefined();
  });

  it('deleteNodes 清掉被删节点的覆盖条目', () => {
    useCanvasStore.getState().applyTransientPositions([{ id: 'n1', x: 30, y: 40 }]);
    useCanvasStore.getState().applyRemoteOp({ kind: 'deleteNodes', nodeIds: ['n1'] });
    expect(overlayOf('n1')).toBeUndefined();
  });
});
