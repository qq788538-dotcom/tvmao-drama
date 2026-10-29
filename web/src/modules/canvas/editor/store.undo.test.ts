import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setCanvasOpSink, useCanvasStore, type CanvasOp } from './store';
import type { CanvasNode } from './types';
import {
  configureCanvasPersistence,
  flushCanvasMutations,
  resetCanvasPersistenceForTest,
  seedCanvasBaselines,
  setCanvasOwnerToken,
} from './persistence/canvasMutations';

// 撤销语义（P1、P18）：undo/redo 不再把整张画布原样回滚。快照与当前都有的节点，
// 生成结果等服务端权威字段保留当前值；撤销删除而重新出现的节点照用快照值。
// 差量广播对已存在的节点走 patchNode（持久化层对基线 diff），不再整节点 upsert。

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

function resetStore(nodes: CanvasNode[] = []) {
  useCanvasStore.setState({
    nodes,
    connections: [],
    view: { x: 0, y: 0, zoom: 1 },
    selectedIds: new Set<string>(),
    undoStack: [],
    redoStack: [],
    readOnly: false,
  });
}

// 模拟一次生成成功的回填（与 GenImageNode 成功分支写的字段一致）。
function finishGeneration(id: string, url: string) {
  const s = useCanvasStore.getState();
  s.updateNodeSettings(id, {
    history: [{ url, prompt: '一只猫' }],
    historyIndex: 0,
    pendingGen: undefined,
    assetId: 7,
    kind: 'image',
  });
  s.updateNode(id, { content: url, status: 'succeeded', error: undefined, taskId: undefined });
}

// 「移动 B」的一次完整拖动：commit 撤销点后改几何（与 NodeShell 拖动同一顺序）。
function dragNode(id: string, dx: number, dy: number) {
  const s = useCanvasStore.getState();
  s.commit();
  s.moveNodes([id], dx, dy);
}

const getNode = (id: string) => useCanvasStore.getState().nodes.find((n) => n.id === id)!;

describe('undo/redo 保留服务端权威字段', () => {
  let ops: CanvasOp[];

  beforeEach(() => {
    ops = [];
    setCanvasOpSink((op) => ops.push(op));
    resetStore([
      node('A', { status: 'generating', taskId: 42, settings: { prompt: '一只猫', pendingGen: { prompt: '一只猫' } } }),
      node('B', { type: 'text-node', x: 500 }),
    ]);
  });

  afterEach(() => setCanvasOpSink(null));

  it('移动 B → A 出图 → 撤销：A 的结果保留，B 回到原位', () => {
    dragNode('B', 100, 50);
    finishGeneration('A', 'media://a1');
    ops.length = 0;

    useCanvasStore.getState().undo();

    const a = getNode('A');
    expect(a.content).toBe('media://a1');
    expect(a.status).toBe('succeeded');
    expect(a.taskId).toBeUndefined();
    expect(a.settings!.history).toEqual([{ url: 'media://a1', prompt: '一只猫' }]);
    expect(a.settings!.historyIndex).toBe(0);
    expect(a.settings!.assetId).toBe(7);
    expect(a.settings!.kind).toBe('image');
    // 当前已经没有 pendingGen，不能被快照里的旧值复活。
    expect(a.settings!.pendingGen).toBeUndefined();
    // 用户字段不受影响。
    expect(a.settings!.prompt).toBe('一只猫');
    expect(getNode('B').x).toBe(500);

    // 只广播 B：A 合并后与当前逐字段相同，引用不变，直接跳过。
    expect(ops).toHaveLength(1);
    expect(ops[0]).toMatchObject({ kind: 'patchNode', node: { id: 'B', x: 500 } });
  });

  it('A 合并后与当前相同时保留引用（组件不因撤销重渲染）', () => {
    dragNode('B', 100, 0);
    finishGeneration('A', 'media://a1');
    const aBefore = getNode('A');
    useCanvasStore.getState().undo();
    expect(getNode('A')).toBe(aBefore);
  });

  it('撤销后重做：B 回到移动后的位置，A 的结果仍在', () => {
    dragNode('B', 100, 0);
    useCanvasStore.getState().undo();
    finishGeneration('A', 'media://a1');
    ops.length = 0;

    useCanvasStore.getState().redo();

    expect(getNode('B').x).toBe(600);
    expect(getNode('A').content).toBe('media://a1');
    expect(getNode('A').status).toBe('succeeded');
    expect(ops).toEqual([expect.objectContaining({ kind: 'patchNode', node: expect.objectContaining({ id: 'B' }) })]);
  });

  it('同一节点：用户字段撤回，服务端字段保留', () => {
    const s = useCanvasStore.getState();
    // 拖动 A 本身，期间 A 出图。
    s.commit();
    s.moveNodes(['A'], 10, 20);
    s.updateNodeSettings('A', { prompt: '改过的词' });
    finishGeneration('A', 'media://a1');
    useCanvasStore.getState().undo();

    const a = getNode('A');
    expect(a.x).toBe(0);
    expect(a.y).toBe(0);
    expect(a.settings!.prompt).toBe('一只猫');
    expect(a.content).toBe('media://a1');
    expect(a.status).toBe('succeeded');
  });

  it('撤销删除：重新出现的节点照用快照值，全部字段恢复', () => {
    const s = useCanvasStore.getState();
    finishGeneration('A', 'media://a1');
    s.updateNodeSettings('A', { unsaved: true, genTaskId: 9 });
    const aBeforeDelete = getNode('A');
    s.deleteNodes(['A']);
    expect(useCanvasStore.getState().nodes.some((n) => n.id === 'A')).toBe(false);
    ops.length = 0;

    useCanvasStore.getState().undo();

    expect(getNode('A')).toEqual(aBeforeDelete);
    expect(ops).toEqual([{ kind: 'upsertNode', node: aBeforeDelete }]);
  });

  it('快照里在生成、当前也在生成：taskId 跟当前走，撤销后照常续轮询', () => {
    dragNode('B', 10, 0);
    useCanvasStore.getState().updateNode('A', { taskId: 43, progress: 30 });
    useCanvasStore.getState().undo();
    const a = getNode('A');
    expect(a.status).toBe('generating');
    expect(a.taskId).toBe(43);
    expect(a.progress).toBe(30);
  });
});

describe('undo 经持久化层只发变化字段', () => {
  let patchNode: ReturnType<typeof vi.fn>;
  let upsertNode: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    resetCanvasPersistenceForTest();
    patchNode = vi.fn(async () => ({ version: 1 }));
    upsertNode = vi.fn(async () => ({ version: 1 }));
    const a = node('A', { status: 'generating', taskId: 42 });
    const b = node('B', { type: 'text-node', x: 500 });
    resetStore([a, b]);
    seedCanvasBaselines([a, b]);
    configureCanvasPersistence({ projectID: 42, patchNode, upsertNode });
    setCanvasOwnerToken('owner-token');
  });

  afterEach(() => resetCanvasPersistenceForTest());

  it('移动 B → A 出图 → 撤销：只发 B 的几何 patch，不整节点 upsert', async () => {
    dragNode('B', 100, 50);
    finishGeneration('A', 'media://a1');
    // 拖动松手的几何 patch 与 A 的结果回填都已落库：基线 = 服务端此刻的状态。
    seedCanvasBaselines(useCanvasStore.getState().nodes);

    useCanvasStore.getState().undo();
    await flushCanvasMutations();

    expect(upsertNode).not.toHaveBeenCalled();
    expect(patchNode).toHaveBeenCalledTimes(1);
    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'B', { x: 500, y: 0 });
  });

  it('没有基线的节点（未播种）退回整节点 upsert，与 persistNodeById 一致', async () => {
    resetCanvasPersistenceForTest();
    configureCanvasPersistence({ projectID: 42, patchNode, upsertNode });
    setCanvasOwnerToken('owner-token');
    dragNode('B', 100, 50);

    useCanvasStore.getState().undo();
    await flushCanvasMutations();

    expect(patchNode).not.toHaveBeenCalled();
    expect(upsertNode).toHaveBeenCalledTimes(1);
    expect(upsertNode).toHaveBeenCalledWith(42, 'owner-token', expect.objectContaining({ id: 'B', x: 500 }));
  });
});

describe('派生索引与数组一致', () => {
  beforeEach(() => {
    setCanvasOpSink(null);
    resetStore();
  });

  function expectIndexesConsistent() {
    const s = useCanvasStore.getState();
    expect(s.nodesById.size).toBe(s.nodes.length);
    for (const n of s.nodes) expect(s.nodesById.get(n.id)).toBe(n);
    const incomingCount = [...s.incomingByNode.values()].reduce((sum, l) => sum + l.length, 0);
    expect(incomingCount).toBe(s.connections.length);
    for (const [to, list] of s.incomingByNode) {
      expect(list).toEqual(s.connections.filter((c) => c.to === to));
    }
  }

  it('每个写 nodes/connections 的 action 之后索引都与数组一致', () => {
    const s = () => useCanvasStore.getState();
    const a = s().addNode('input-image', { x: 0, y: 0 });
    expectIndexesConsistent();
    const b = s().addNode('gen-image', { x: 300, y: 0 });
    expectIndexesConsistent();
    s().addConnection(a, b, 'image');
    expectIndexesConsistent();
    s().updateNode(a, { content: 'https://x/a.png' });
    expectIndexesConsistent();
    s().updateNodeSettings(b, { prompt: 'p' });
    expectIndexesConsistent();
    s().moveNodes([a, b], 5, 5);
    expectIndexesConsistent();
    s().resizeNode(b, 400, 400);
    expectIndexesConsistent();
    s().duplicateNodes([a, b]);
    expectIndexesConsistent();
    const payload = s().buildCopyPayload([a, b])!;
    s().pasteNodes(payload.nodes, payload.connections, { x: 1000, y: 1000 });
    expectIndexesConsistent();
    s().addLinkedInputImageNodes(a, [{ x: 0, y: 600, width: 100, height: 100 }]);
    expectIndexesConsistent();
    s().removeConnection(s().connections[0].id);
    expectIndexesConsistent();
    s().deleteNodes([b]);
    expectIndexesConsistent();
    s().undo();
    expectIndexesConsistent();
    s().undo();
    expectIndexesConsistent();
    s().redo();
    expectIndexesConsistent();
    s().load({ nodes: [node('x')], connections: [] }, { preserveUiState: true });
    expectIndexesConsistent();
    s().load({ nodes: [], connections: [] });
    expectIndexesConsistent();
  });

  it('直接 setState 写 nodes/connections 也同步索引', () => {
    useCanvasStore.setState({
      nodes: [node('p'), node('q')],
      connections: [{ id: 'c1', from: 'p', to: 'q' }],
    });
    expectIndexesConsistent();
    expect(useCanvasStore.getState().incomingByNode.get('q')).toHaveLength(1);
    useCanvasStore.setState((st) => ({ nodes: st.nodes.filter((n) => n.id !== 'p') }));
    expectIndexesConsistent();
  });

  it('不写 nodes 的 set 不重建索引', () => {
    useCanvasStore.setState({ nodes: [node('p')] });
    const before = useCanvasStore.getState().nodesById;
    useCanvasStore.getState().setView({ x: 1, y: 2, zoom: 1 });
    useCanvasStore.getState().selectNode('p');
    expect(useCanvasStore.getState().nodesById).toBe(before);
  });
});
