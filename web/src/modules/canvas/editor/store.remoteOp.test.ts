import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  clearDeferredRemoteOps,
  setCanvasOpSink,
  setPendingLocalPatchProvider,
  setRemoteNodeStateSink,
  useCanvasStore,
  type CanvasOp,
  type RemoteCanvasOp,
} from './store';
import {
  beginInteraction,
  endInteraction,
  resetInteractionRegistry,
} from './interactionRegistry';
import type { CanvasNode } from './types';

// applyRemoteOp 的行为测试：远端（队友）op 合并进本地 store 的语义。
// 三条铁律：合并语义与后端 canvasstate 同构、绝不回声（不触发 emitCanvasOp）、
// 本地在途交互的节点先挂起再落地。

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

function resetStore() {
  useCanvasStore.setState({
    nodes: [],
    connections: [],
    view: { x: 0, y: 0, zoom: 1 },
    selectedIds: new Set<string>(),
    undoStack: [],
    redoStack: [],
    readOnly: false,
  });
  clearDeferredRemoteOps();
  resetInteractionRegistry();
  setPendingLocalPatchProvider(null);
}

function apply(op: RemoteCanvasOp) {
  useCanvasStore.getState().applyRemoteOp(op);
}

function nodeById(id: string): CanvasNode | undefined {
  return useCanvasStore.getState().nodes.find((n) => n.id === id);
}

describe('applyRemoteOp 六种 kind 的合并语义', () => {
  let ops: CanvasOp[];

  beforeEach(() => {
    resetStore();
    ops = [];
    setCanvasOpSink((op) => ops.push(op));
    setRemoteNodeStateSink(null);
  });

  it('upsertNode 新增不存在的节点', () => {
    apply({ kind: 'upsertNode', node: node('n1', { x: 10 }) });
    expect(nodeById('n1')).toMatchObject({ id: 'n1', x: 10 });
  });

  it('upsertNode 对已存在节点是整替换（不合并旧字段）', () => {
    useCanvasStore.setState({ nodes: [node('n1', { content: 'old', settings: { prompt: 'a' } })] });
    apply({ kind: 'upsertNode', node: node('n1', { x: 99 }) });
    const n = nodeById('n1')!;
    expect(n.x).toBe(99);
    expect(n.content).toBeUndefined();
    expect(n.settings).toEqual({});
  });

  it('patchNode 递归合并对象字段，数组与标量整体替换', () => {
    useCanvasStore.setState({
      nodes: [node('n1', {
        content: 'old',
        settings: { prompt: 'a', params: { seed: 1, ratio: '1:1' }, history: ['h1'] },
      })],
    });
    apply({
      kind: 'patchNode',
      nodeId: 'n1',
      patch: {
        content: 'new',
        settings: { params: { seed: 2 }, history: ['h2'] },
      },
    });
    expect(nodeById('n1')).toMatchObject({
      content: 'new',
      settings: { prompt: 'a', params: { seed: 2, ratio: '1:1' }, history: ['h2'] },
    });
  });

  it('patchNode 的 null 是删键（RFC7386，与后端 mergeRawValue 同构）', () => {
    useCanvasStore.setState({
      nodes: [node('n1', { taskId: 7, settings: { params: { seed: 1 }, prompt: 'p' } })],
    });
    apply({ kind: 'patchNode', nodeId: 'n1', patch: { taskId: null, settings: { params: null } } });
    expect(nodeById('n1')!.settings).toEqual({ prompt: 'p' });
    expect('taskId' in nodeById('n1')!).toBe(false);
  });

  it('patchNode 的数组整体替换、非对象值覆盖原对象', () => {
    useCanvasStore.setState({ nodes: [node('n1', { settings: { params: { seed: 1 }, refs: ['a', 'b'] } })] });
    apply({ kind: 'patchNode', nodeId: 'n1', patch: { settings: { params: 'flat', refs: ['c'] } } });
    expect(nodeById('n1')!.settings).toEqual({ params: 'flat', refs: ['c'] });
  });

  it('落地的数据与帧对象脱钩（外部改帧不影响画布）', () => {
    const incoming = node('n1', { settings: { params: { seed: 1 } } });
    apply({ kind: 'upsertNode', node: incoming });
    (incoming.settings!.params as Record<string, unknown>).seed = 999;
    expect(nodeById('n1')!.settings).toEqual({ params: { seed: 1 } });
  });

  it('patchNode 命中不存在的节点时静默忽略', () => {
    apply({ kind: 'patchNode', nodeId: 'ghost', patch: { x: 1 } });
    expect(useCanvasStore.getState().nodes).toHaveLength(0);
  });

  it('deleteNodes 删节点、连带删两端连线、并从选中集移出', () => {
    useCanvasStore.setState({
      nodes: [node('n1'), node('n2')],
      connections: [
        { id: 'e1', from: 'n1', to: 'n2' },
        { id: 'e2', from: 'n2', to: 'n1' },
      ],
      selectedIds: new Set(['n1', 'n2']),
    });
    apply({ kind: 'deleteNodes', nodeIds: ['n1'] });
    expect(useCanvasStore.getState().nodes.map((n) => n.id)).toEqual(['n2']);
    expect(useCanvasStore.getState().connections).toHaveLength(0);
    expect([...useCanvasStore.getState().selectedIds]).toEqual(['n2']);
  });

  it('patchPositions 只改几何字段', () => {
    useCanvasStore.setState({ nodes: [node('n1', { content: 'keep' }), node('n2')] });
    apply({ kind: 'patchPositions', positions: [{ id: 'n1', x: 50, y: 60, width: 300 }] });
    expect(nodeById('n1')).toMatchObject({ x: 50, y: 60, width: 300, height: 280, content: 'keep' });
    expect(nodeById('n2')).toMatchObject({ x: 0, y: 0 });
  });

  it('upsertConnection 按 id 新增或整替换', () => {
    apply({ kind: 'upsertConnection', connection: { id: 'e1', from: 'a', to: 'b' } });
    apply({ kind: 'upsertConnection', connection: { id: 'e1', from: 'a', to: 'c', inputType: 'image' } });
    expect(useCanvasStore.getState().connections).toEqual([
      { id: 'e1', from: 'a', to: 'c', inputType: 'image' },
    ]);
  });

  it('deleteConnection 按 id 删除', () => {
    useCanvasStore.setState({ connections: [{ id: 'e1', from: 'a', to: 'b' }] });
    apply({ kind: 'deleteConnection', connectionId: 'e1' });
    expect(useCanvasStore.getState().connections).toHaveLength(0);
  });

  it('六种 kind 一律不回声到 emitCanvasOp（防广播风暴）', () => {
    useCanvasStore.setState({
      nodes: [node('n1')],
      connections: [{ id: 'e1', from: 'n1', to: 'n2' }],
    });
    apply({ kind: 'upsertNode', node: node('n2') });
    apply({ kind: 'patchNode', nodeId: 'n1', patch: { x: 5 } });
    apply({ kind: 'patchPositions', positions: [{ id: 'n1', x: 9 }] });
    apply({ kind: 'upsertConnection', connection: { id: 'e2', from: 'n1', to: 'n2' } });
    apply({ kind: 'deleteConnection', connectionId: 'e2' });
    apply({ kind: 'deleteNodes', nodeIds: ['n2'] });
    expect(ops).toEqual([]);
  });

  it('远端 op 不清空选中、不动 undo/redo 栈', () => {
    useCanvasStore.setState({
      nodes: [node('n1'), node('n2')],
      selectedIds: new Set(['n2']),
      undoStack: [{ nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } }],
      redoStack: [{ nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } }],
    });
    apply({ kind: 'patchNode', nodeId: 'n1', patch: { x: 3 } });
    apply({ kind: 'upsertNode', node: node('n3') });
    expect([...useCanvasStore.getState().selectedIds]).toEqual(['n2']);
    expect(useCanvasStore.getState().undoStack).toHaveLength(1);
    expect(useCanvasStore.getState().redoStack).toHaveLength(1);
  });

  it('readOnly（观看态）下远端 op 照常落地——服务端状态才是权威', () => {
    useCanvasStore.setState({ nodes: [node('n1')], readOnly: true });
    apply({ kind: 'patchNode', nodeId: 'n1', patch: { x: 7 } });
    expect(nodeById('n1')!.x).toBe(7);
  });

  // sink 回吐的必须是**服务端做的那一步动作**，不能是合并后的 store 节点：
  // store 节点掺着本地未发出的编辑，拿它当基线会让那些编辑在 diff 里凭空消失。
  it('落地后回吐 op 本身（patch 吐 patch、upsert 吐整节点、删除吐 null）', () => {
    const sink = vi.fn();
    setRemoteNodeStateSink(sink);
    useCanvasStore.setState({ nodes: [node('n1', { settings: { prompt: 'a' } })] });

    apply({ kind: 'patchNode', nodeId: 'n1', patch: { settings: { ratio: '1:1' } } });
    expect(sink).toHaveBeenLastCalledWith('n1', { kind: 'patch', patch: { settings: { ratio: '1:1' } } });

    apply({ kind: 'patchPositions', positions: [{ id: 'n1', x: 8 }] });
    expect(sink).toHaveBeenLastCalledWith('n1', { kind: 'patch', patch: { x: 8 } });

    const whole = node('n1', { content: 'w' });
    apply({ kind: 'upsertNode', node: whole });
    expect(sink).toHaveBeenLastCalledWith('n1', { kind: 'node', node: expect.objectContaining({ content: 'w' }) });

    apply({ kind: 'deleteNodes', nodeIds: ['n1'] });
    expect(sink).toHaveBeenLastCalledWith('n1', null);
    setRemoteNodeStateSink(null);
  });
});

describe('applyRemoteOp 的在途交互保护', () => {
  let sink: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    resetStore();
    setCanvasOpSink(null);
    sink = vi.fn();
    setRemoteNodeStateSink(sink);
  });

  it('拖拽中的节点：远端 patch 挂起，松手后才落地', () => {
    useCanvasStore.setState({ nodes: [node('n1', { x: 0 })] });
    beginInteraction('n1', 'drag');

    apply({ kind: 'patchNode', nodeId: 'n1', patch: { settings: { prompt: 'remote' } } });
    expect(nodeById('n1')!.settings).toEqual({});
    // 挂起期间基线不能推进：否则本地随后的 diff 会把队友这个字段又改回去。
    expect(sink).not.toHaveBeenCalled();

    endInteraction('n1', 'drag');
    expect(nodeById('n1')!.settings).toEqual({ prompt: 'remote' });
    expect(sink).toHaveBeenCalledWith('n1', { kind: 'patch', patch: { settings: { prompt: 'remote' } } });
  });

  it('文本编辑中的节点：远端 upsert 挂起，失焦后整替换落地', () => {
    useCanvasStore.setState({ nodes: [node('n1', { type: 'text-node', content: 'local' })] });
    beginInteraction('n1', 'text');

    apply({ kind: 'upsertNode', node: node('n1', { type: 'text-node', content: 'remote' }) });
    expect(nodeById('n1')!.content).toBe('local');

    endInteraction('n1', 'text');
    expect(nodeById('n1')!.content).toBe('remote');
  });

  it('多帧挂起按字段合并，同字段后到覆盖先到（LWW）', () => {
    useCanvasStore.setState({ nodes: [node('n1')] });
    beginInteraction('n1', 'drag');

    apply({ kind: 'patchNode', nodeId: 'n1', patch: { settings: { prompt: 'first', ratio: '1:1' } } });
    apply({ kind: 'patchNode', nodeId: 'n1', patch: { settings: { prompt: 'second' } } });

    endInteraction('n1', 'drag');
    expect(nodeById('n1')!.settings).toEqual({ prompt: 'second', ratio: '1:1' });
  });

  it('挂起期间来的整节点 upsert 作废先前累积的 patch', () => {
    useCanvasStore.setState({ nodes: [node('n1')] });
    beginInteraction('n1', 'drag');

    apply({ kind: 'patchNode', nodeId: 'n1', patch: { settings: { prompt: 'stale' } } });
    apply({ kind: 'upsertNode', node: node('n1', { content: 'whole' }) });
    apply({ kind: 'patchNode', nodeId: 'n1', patch: { x: 42 } });

    endInteraction('n1', 'drag');
    expect(nodeById('n1')).toMatchObject({ content: 'whole', x: 42, settings: {} });
  });

  it('拖拽中的节点：远端 patchPositions 也挂起，其余节点照常落地', () => {
    useCanvasStore.setState({ nodes: [node('n1'), node('n2')] });
    beginInteraction('n1', 'drag');

    apply({ kind: 'patchPositions', positions: [{ id: 'n1', x: 100 }, { id: 'n2', x: 200 }] });
    expect(nodeById('n1')!.x).toBe(0);
    expect(nodeById('n2')!.x).toBe(200);

    endInteraction('n1', 'drag');
    expect(nodeById('n1')!.x).toBe(100);
  });

  it('多种交互叠加时，最后一种结束才 flush', () => {
    useCanvasStore.setState({ nodes: [node('n1')] });
    beginInteraction('n1', 'drag');
    beginInteraction('n1', 'text');

    apply({ kind: 'patchNode', nodeId: 'n1', patch: { x: 5 } });
    endInteraction('n1', 'drag');
    expect(nodeById('n1')!.x).toBe(0);

    endInteraction('n1', 'text');
    expect(nodeById('n1')!.x).toBe(5);
  });

  it('远端删除不挂起：立即生效并丢弃该节点已挂起的 op', () => {
    useCanvasStore.setState({ nodes: [node('n1')] });
    beginInteraction('n1', 'drag');
    apply({ kind: 'patchNode', nodeId: 'n1', patch: { x: 5 } });

    apply({ kind: 'deleteNodes', nodeIds: ['n1'] });
    expect(useCanvasStore.getState().nodes).toHaveLength(0);

    endInteraction('n1', 'drag');
    expect(useCanvasStore.getState().nodes).toHaveLength(0);
  });

  // 本端在交互期间提交的字段，在服务端落地得比挂起的那帧更晚，LWW 序上不该被
  // 旧帧盖回去；而且这次覆盖没有任何后续广播能纠正（自己的 op 回来按 source_mark
  // 跳过）。所以 flush 前要剔掉与本地 pending patch 重叠的字段。
  it('flush 时剔除与本地 pending patch 重叠的字段（本地打的字不被旧帧覆盖）', () => {
    useCanvasStore.setState({ nodes: [node('n1', { type: 'text-node', settings: { text: 'old' } })] });
    setPendingLocalPatchProvider((id) => (id === 'n1' ? { settings: { text: 'local typed' } } : null));
    beginInteraction('n1', 'text');

    // 队友在我打字期间改了同一段文字 + 另一个不冲突的字段。
    apply({
      kind: 'patchNode',
      nodeId: 'n1',
      patch: { settings: { text: 'remote', color: 'red' } },
    });
    useCanvasStore.setState({
      nodes: [node('n1', { type: 'text-node', settings: { text: 'local typed' } })],
    });

    endInteraction('n1', 'text');

    const settings = nodeById('n1')!.settings!;
    expect(settings.text).toBe('local typed');
    // 不冲突的字段照常落地——保护的是重叠字段，不是整帧丢弃。
    expect(settings.color).toBe('red');
    // 基线也只收到实际落地的那部分。
    expect(sink).toHaveBeenCalledWith('n1', { kind: 'patch', patch: { settings: { color: 'red' } } });
  });

  it('挂起的整节点 upsert 落地后，本地 pending 字段盖回 store、基线仍是服务端那份', () => {
    useCanvasStore.setState({ nodes: [node('n1', { type: 'text-node', settings: { text: 'local typed' } })] });
    setPendingLocalPatchProvider(() => ({ settings: { text: 'local typed' } }));
    beginInteraction('n1', 'text');

    apply({
      kind: 'upsertNode',
      node: node('n1', { type: 'text-node', content: 'w', settings: { text: 'remote' } }),
    });
    endInteraction('n1', 'text');

    expect(nodeById('n1')!.settings).toEqual({ text: 'local typed' });
    expect(nodeById('n1')!.content).toBe('w');
    expect(sink).toHaveBeenCalledWith('n1', {
      kind: 'node',
      node: expect.objectContaining({ settings: { text: 'remote' } }),
    });
  });

  it('挂起 patch 的字段被本地 pending 全覆盖时，什么都不落地', () => {
    useCanvasStore.setState({ nodes: [node('n1', { settings: { prompt: 'local' } })] });
    setPendingLocalPatchProvider(() => ({ settings: { prompt: 'local' } }));
    beginInteraction('n1', 'text');

    apply({ kind: 'patchNode', nodeId: 'n1', patch: { settings: { prompt: 'remote' } } });
    endInteraction('n1', 'text');

    expect(nodeById('n1')!.settings).toEqual({ prompt: 'local' });
    expect(sink).not.toHaveBeenCalled();
  });

  it('本端删除节点后，其挂起的远端 upsert 不会把它复活', () => {
    useCanvasStore.setState({ nodes: [node('n1')] });
    beginInteraction('n1', 'drag');
    apply({ kind: 'upsertNode', node: node('n1', { content: 'remote' }) });

    useCanvasStore.getState().deleteNodes(['n1']);
    endInteraction('n1', 'drag');

    expect(useCanvasStore.getState().nodes).toHaveLength(0);
  });

  it('load（整快照重载）清空挂起队列', () => {
    useCanvasStore.setState({ nodes: [node('n1')] });
    beginInteraction('n1', 'drag');
    apply({ kind: 'patchNode', nodeId: 'n1', patch: { x: 5 } });

    useCanvasStore.getState().load({ nodes: [node('n1', { x: 1 })], connections: [] });
    endInteraction('n1', 'drag');
    expect(nodeById('n1')!.x).toBe(1);
  });
});
