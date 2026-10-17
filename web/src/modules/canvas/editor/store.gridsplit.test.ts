import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { setCanvasOpSink, useCanvasStore, type CanvasOp } from './store';

// 宫格切分落地侧 addLinkedInputImageNodes 的行为测试。钉死三条约定：
//   1. 整批一次 commit——25 格切分按一次撤销整体收回，不许按 25 下 Ctrl+Z；
//   2. 每个新节点都从 sourceId 连一条入边——用户点名要连回源图，来源可追溯；
//   3. 返回的 id 列表与入参 rects 同序——调用方按下标对应格子上传，顺序错位
//      就是把左上角的图传进右下角的节点。
// 同时覆盖 op 广播（先节点后连线，保证持久化队列串行执行时连线两端已存在）
// 与 readOnly / 空 rects / sourceId 不存在三个边界。

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
}

// 预置一个源节点（宫格切分的源图）并返回其 id。
function addSourceNode(): string {
  return useCanvasStore.getState().addNode('input-image', { x: 100, y: 100 });
}

const RECTS = [
  { x: 400, y: 0, width: 200, height: 150 },
  { x: 620, y: 0, width: 200, height: 150 },
];

describe('addLinkedInputImageNodes 宫格切分落地', () => {
  beforeEach(() => {
    resetStore();
  });

  afterEach(() => {
    setCanvasOpSink(null);
  });

  it('按矩形批量建 input-image 节点并各自连回源节点,选中且返回序与入参同序', () => {
    const sourceId = addSourceNode();
    const before = useCanvasStore.getState();
    const nodeCountBefore = before.nodes.length;
    const connCountBefore = before.connections.length;

    const ids = useCanvasStore.getState().addLinkedInputImageNodes(sourceId, RECTS);

    expect(ids).toHaveLength(2);
    const s = useCanvasStore.getState();
    expect(s.nodes).toHaveLength(nodeCountBefore + 2);
    expect(s.connections).toHaveLength(connCountBefore + 2);

    // 返回序与入参序一致：第 i 个 id 对应第 i 个矩形（上传按下标对应格子）。
    ids.forEach((id, i) => {
      const node = s.nodes.find((n) => n.id === id)!;
      expect(node).toBeDefined();
      expect(node.type).toBe('input-image');
      expect(node.status).toBe('idle');
      expect(node.x).toBe(RECTS[i].x);
      expect(node.y).toBe(RECTS[i].y);
      expect(node.width).toBe(RECTS[i].width);
      expect(node.height).toBe(RECTS[i].height);
      // 每个新节点一条来自源节点的入边。
      const inbound = s.connections.filter((c) => c.to === id);
      expect(inbound).toHaveLength(1);
      expect(inbound[0].from).toBe(sourceId);
    });

    // 选中恰好是这批新节点。
    expect(s.selectedIds).toEqual(new Set(ids));
  });

  it('单次 undo 收回整批(一次 commit,不按格子数撤销)', () => {
    const sourceId = addSourceNode();
    const nodesBefore = useCanvasStore.getState().nodes.length;
    const connsBefore = useCanvasStore.getState().connections.length;

    useCanvasStore.getState().addLinkedInputImageNodes(sourceId, RECTS);
    useCanvasStore.getState().undo();

    const s = useCanvasStore.getState();
    expect(s.nodes).toHaveLength(nodesBefore);
    expect(s.connections).toHaveLength(connsBefore);
  });

  it('广播 op:先 2 条 upsertNode 再 2 条 upsertConnection', () => {
    const sourceId = addSourceNode();
    const ops: CanvasOp[] = [];
    setCanvasOpSink((op) => ops.push(op));

    const ids = useCanvasStore.getState().addLinkedInputImageNodes(sourceId, RECTS);

    expect(ops.map((op) => op.kind)).toEqual([
      'upsertNode',
      'upsertNode',
      'upsertConnection',
      'upsertConnection',
    ]);
    const nodeOps = ops.slice(0, 2) as Array<Extract<CanvasOp, { kind: 'upsertNode' }>>;
    expect(nodeOps.map((op) => op.node.id)).toEqual(ids);
    const connOps = ops.slice(2) as Array<Extract<CanvasOp, { kind: 'upsertConnection' }>>;
    for (const op of connOps) expect(op.connection.from).toBe(sourceId);
    expect(connOps.map((op) => op.connection.to)).toEqual(ids);
  });

  it('readOnly 时返回 [] 且状态不变', () => {
    const sourceId = addSourceNode();
    useCanvasStore.setState({ readOnly: true });
    const nodesBefore = useCanvasStore.getState().nodes;
    const connsBefore = useCanvasStore.getState().connections;

    const ids = useCanvasStore.getState().addLinkedInputImageNodes(sourceId, RECTS);

    expect(ids).toEqual([]);
    expect(useCanvasStore.getState().nodes).toBe(nodesBefore);
    expect(useCanvasStore.getState().connections).toBe(connsBefore);
  });

  it('rects 为空数组时返回 [] 不建节点', () => {
    const sourceId = addSourceNode();
    const nodesBefore = useCanvasStore.getState().nodes.length;

    const ids = useCanvasStore.getState().addLinkedInputImageNodes(sourceId, []);

    expect(ids).toEqual([]);
    expect(useCanvasStore.getState().nodes).toHaveLength(nodesBefore);
  });

  it('sourceId 不存在时返回 [] 且不建任何节点/连线', () => {
    addSourceNode();
    const nodesBefore = useCanvasStore.getState().nodes.length;
    const connsBefore = useCanvasStore.getState().connections.length;

    const ids = useCanvasStore.getState().addLinkedInputImageNodes('n-missing', RECTS);

    expect(ids).toEqual([]);
    expect(useCanvasStore.getState().nodes).toHaveLength(nodesBefore);
    expect(useCanvasStore.getState().connections).toHaveLength(connsBefore);
  });
});
