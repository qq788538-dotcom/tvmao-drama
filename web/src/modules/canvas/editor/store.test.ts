import { beforeEach, describe, expect, it, vi } from 'vitest';
import { setCanvasOpSink, useCanvasStore, type AudioEditState, type CanvasOp } from './store';
import type { CanvasNode, CanvasSnapshot } from './types';

// 结构性 op 的持久化收口测试：验证 store 在增删节点/连线、duplicate、undo/redo
// 后广播正确的语义 op，且 readOnly / 无 sink 时不广播、不抛错。这是「用户动作
// 触发局部保存」模型的核心——十几个调用点的持久化都靠 store 这一层收口。

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

describe('canvas store 结构性 op 广播', () => {
  let ops: CanvasOp[];

  beforeEach(() => {
    resetStore();
    ops = [];
    setCanvasOpSink((op) => ops.push(op));
  });

  it('addNode 广播 upsertNode', () => {
    const id = useCanvasStore.getState().addNode('text-node', { x: 100, y: 100 });
    expect(ops).toHaveLength(1);
    expect(ops[0]).toMatchObject({ kind: 'upsertNode', node: { id, type: 'text-node' } });
  });

  it('addConnection 广播 upsertConnection', () => {
    const a = useCanvasStore.getState().addNode('text-node', { x: 0, y: 0 });
    const b = useCanvasStore.getState().addNode('gen-image', { x: 200, y: 0 });
    ops.length = 0;
    useCanvasStore.getState().addConnection(a, b, 'image');
    expect(ops).toHaveLength(1);
    expect(ops[0]).toMatchObject({ kind: 'upsertConnection', connection: { from: a, to: b, inputType: 'image' } });
  });

  it('重复 addConnection 幂等，不重复广播', () => {
    const a = useCanvasStore.getState().addNode('text-node', { x: 0, y: 0 });
    const b = useCanvasStore.getState().addNode('gen-image', { x: 200, y: 0 });
    useCanvasStore.getState().addConnection(a, b, 'image');
    ops.length = 0;
    useCanvasStore.getState().addConnection(a, b, 'image');
    expect(ops).toHaveLength(0);
  });

  it('deleteNodes 只广播实际存在的节点', () => {
    const a = useCanvasStore.getState().addNode('text-node', { x: 0, y: 0 });
    ops.length = 0;
    useCanvasStore.getState().deleteNodes([a, 'n-missing']);
    expect(ops).toEqual([{ kind: 'deleteNodes', ids: [a] }]);
  });

  it('duplicateNodes 为每个新节点广播 upsertNode', () => {
    const a = useCanvasStore.getState().addNode('text-node', { x: 0, y: 0 });
    ops.length = 0;
    useCanvasStore.getState().duplicateNodes([a]);
    expect(ops).toHaveLength(1);
    expect(ops[0].kind).toBe('upsertNode');
    const dup = ops[0] as Extract<CanvasOp, { kind: 'upsertNode' }>;
    expect(dup.node.id).not.toBe(a);
  });

  // 「拷贝节点」要求内容 1:1。settings 必须是深拷贝——history/params 都是嵌套结构，
  // 共享引用的话将来任何就地改写都会串到另一个节点。
  it('duplicateNodes 1:1 复制内容,settings 深拷贝互不影响', () => {
    const a = useCanvasStore.getState().addNode('gen-image', { x: 0, y: 0 });
    useCanvasStore.getState().updateNode(a, { content: 'media://v2', status: 'succeeded' });
    useCanvasStore.getState().updateNodeSettings(a, {
      prompt: '一只橘猫',
      modelId: 'image-model',
      ratio: '16:9',
      history: [{ url: 'media://v1', prompt: '一只猫' }, { url: 'media://v2', prompt: '一只橘猫' }],
      historyIndex: 1,
    });
    useCanvasStore.getState().duplicateNodes([a]);

    const nodes = useCanvasStore.getState().nodes;
    const src = nodes.find((n) => n.id === a)!;
    const dup = nodes.find((n) => n.id !== a)!;

    expect(dup.content).toBe('media://v2');
    expect(dup.status).toBe('succeeded');
    expect(dup.settings).toEqual(src.settings);
    // 位置错开，否则副本压在原节点上看不见。
    expect(dup.x).toBe(src.x + 32);
    expect(dup.y).toBe(src.y + 32);
    // 深拷贝：改副本的 history 不影响原节点。
    (dup.settings!.history as unknown[]).push({ url: 'media://v3' });
    expect((src.settings!.history as unknown[])).toHaveLength(2);
  });

  // taskId 是「原节点正在做的那件事」，不是节点内容。跟着复制的话副本会去轮询
  // 别人的任务，两个节点抢同一个结果。
  it('duplicateNodes 不复制进行中的任务,状态落定', () => {
    const a = useCanvasStore.getState().addNode('gen-image', { x: 0, y: 0 });
    useCanvasStore.getState().updateNode(a, { status: 'generating', taskId: 42, content: 'media://old' });
    useCanvasStore.getState().updateNodeSettings(a, { pendingGen: { prompt: '正在生成的这句' } });
    useCanvasStore.getState().duplicateNodes([a]);

    const dup = useCanvasStore.getState().nodes.find((n) => n.id !== a)!;
    expect(dup.taskId).toBeUndefined();
    expect(dup.settings!.pendingGen).toBeUndefined();
    // 有内容 → 落成 succeeded，把已有的图当作这个副本的当前结果。
    expect(dup.status).toBe('succeeded');
    expect(dup.content).toBe('media://old');
  });

  it('duplicateNodes 对没有内容的生成中节点落成 idle', () => {
    const a = useCanvasStore.getState().addNode('gen-image', { x: 0, y: 0 });
    useCanvasStore.getState().updateNode(a, { status: 'generating', taskId: 7 });
    useCanvasStore.getState().duplicateNodes([a]);

    const dup = useCanvasStore.getState().nodes.find((n) => n.id !== a)!;
    expect(dup.status).toBe('idle');
    expect(dup.taskId).toBeUndefined();
  });

  // 副本要能立刻重跑同样的生成,就得连着同样的输入。
  it('duplicateNodes 复制入边,上游仍指向原来的输入源', () => {
    const up = useCanvasStore.getState().addNode('input-image', { x: 0, y: 0 });
    const gen = useCanvasStore.getState().addNode('gen-image', { x: 200, y: 0 });
    useCanvasStore.getState().addConnection(up, gen, 'image');
    ops.length = 0;

    useCanvasStore.getState().duplicateNodes([gen]);

    const dupId = [...useCanvasStore.getState().selectedIds][0];
    const conns = useCanvasStore.getState().connections;
    expect(conns).toHaveLength(2);
    const copied = conns.find((c) => c.to === dupId)!;
    // from 仍是原上游节点——副本与原节点共享同一个输入源,不会连带复制上游。
    expect(copied.from).toBe(up);
    expect(copied.inputType).toBe('image');
    // 落库广播:一个节点 + 一条连线。
    expect(ops.filter((o) => o.kind === 'upsertConnection')).toHaveLength(1);
    expect(useCanvasStore.getState().nodes).toHaveLength(3);
  });

  // 出边不复制:否则原下游节点会凭空多出一个输入源。
  it('duplicateNodes 不复制出边', () => {
    const gen = useCanvasStore.getState().addNode('gen-image', { x: 0, y: 0 });
    const down = useCanvasStore.getState().addNode('preview', { x: 200, y: 0 });
    useCanvasStore.getState().addConnection(gen, down, 'image');

    useCanvasStore.getState().duplicateNodes([gen]);

    const conns = useCanvasStore.getState().connections;
    expect(conns).toHaveLength(1);
    expect(conns[0].from).toBe(gen);
    expect(conns[0].to).toBe(down);
  });

  // 多选复制:这批之内的连线要在副本之间重建,让副本自成一套,而不是都挂回原节点。
  it('duplicateNodes 多选时内部连线在副本之间重建,并重写 @ 提及', () => {
    const up = useCanvasStore.getState().addNode('input-image', { x: 0, y: 0 });
    const gen = useCanvasStore.getState().addNode('gen-image', { x: 200, y: 0 });
    useCanvasStore.getState().addConnection(up, gen, 'image');
    useCanvasStore.getState().updateNodeSettings(gen, { prompt: `参考 @[图片:${up}] 的风格` });

    useCanvasStore.getState().duplicateNodes([up, gen]);

    const dupIds = [...useCanvasStore.getState().selectedIds];
    expect(dupIds).toHaveLength(2);
    const nodes = useCanvasStore.getState().nodes;
    const dupUp = nodes.find((n) => n.id !== up && n.type === 'input-image')!;
    const dupGen = nodes.find((n) => n.id !== gen && n.type === 'gen-image')!;

    const internal = useCanvasStore.getState().connections.find((c) => c.to === dupGen.id)!;
    // 内部连线指向副本上游,不是原上游。
    expect(internal.from).toBe(dupUp.id);
    // 提及跟着改指副本,否则断线清理会把这个标记删掉、悄悄改掉用户的提示词。
    expect(dupGen.settings!.prompt).toBe(`参考 @[图片:${dupUp.id}] 的风格`);
  });

  it('removeConnection 广播 deleteConnection', () => {
    const a = useCanvasStore.getState().addNode('text-node', { x: 0, y: 0 });
    const b = useCanvasStore.getState().addNode('gen-image', { x: 200, y: 0 });
    useCanvasStore.getState().addConnection(a, b);
    const connId = useCanvasStore.getState().connections[0].id;
    ops.length = 0;
    useCanvasStore.getState().removeConnection(connId);
    expect(ops).toEqual([{ kind: 'deleteConnection', id: connId }]);
  });

  it('undo 一次 addNode 后广播 deleteNodes 差量', () => {
    const a = useCanvasStore.getState().addNode('text-node', { x: 0, y: 0 });
    ops.length = 0;
    useCanvasStore.getState().undo();
    expect(ops).toEqual([{ kind: 'deleteNodes', ids: [a] }]);
  });

  it('redo 重新加入被 undo 的节点，广播 upsertNode', () => {
    const a = useCanvasStore.getState().addNode('text-node', { x: 0, y: 0 });
    useCanvasStore.getState().undo();
    ops.length = 0;
    useCanvasStore.getState().redo();
    expect(ops).toHaveLength(1);
    expect(ops[0]).toMatchObject({ kind: 'upsertNode', node: { id: a } });
  });

  it('readOnly 下不广播任何 op', () => {
    useCanvasStore.setState({ readOnly: true });
    useCanvasStore.getState().addNode('text-node', { x: 0, y: 0 });
    expect(ops).toHaveLength(0);
  });

  it('未注册 sink 时结构性 action 不抛错', () => {
    setCanvasOpSink(null);
    expect(() => useCanvasStore.getState().addNode('text-node', { x: 0, y: 0 })).not.toThrow();
  });
});

// getConnectedInputs 的媒体类型判定。
//
// 存在的理由(线上 bug):去字幕节点的产物接给转高清节点时,下游报「请连接一个视频」。
// 根因是这里靠「节点类型白名单 + URL 后缀」猜类型,而 subtitle-erase / video-upscale
// 都不在白名单里,且后处理产物的签名 URL 常常没有 .mp4 后缀(对象 key 形如
// gen/task-N-0),于是视频被归进 images。同一类误判在历史面板上修过一次(da968ee),
// 当时漏了画布这条路。
describe('getConnectedInputs 媒体类型判定', () => {
  beforeEach(resetStore);

  // 后处理产物的真实形态:签名 URL 无扩展名。
  const noExtUrl = 'https://cdn.example.com/gen/task-42-0?sig=abc';

  function seed(srcType: string, url: string, settings: Record<string, unknown> = {}) {
    useCanvasStore.setState({
      nodes: [
        {
          id: 'src',
          type: srcType as never,
          x: 0, y: 0, width: 320, height: 260,
          status: 'succeeded',
          content: url,
          settings,
        },
        {
          id: 'dst',
          type: 'video-upscale',
          x: 500, y: 0, width: 320, height: 340,
          status: 'idle',
          settings: {},
        },
      ],
      connections: [{ id: 'c1', from: 'src', to: 'dst', inputType: 'video' }],
    });
  }

  it('擦除字幕产物(无扩展名 URL)判为视频——线上 bug 的直接回归', () => {
    seed('subtitle-erase', noExtUrl);
    const inputs = useCanvasStore.getState().getConnectedInputs('dst');
    expect(inputs.videos).toEqual([noExtUrl]);
    expect(inputs.images).toEqual([]);
    expect(inputs.refs[0]).toMatchObject({ nodeId: 'src', kind: 'video' });
  });

  it('转高清产物(无扩展名 URL)判为视频——支持高清→去字幕反向链式', () => {
    seed('video-upscale', noExtUrl);
    const inputs = useCanvasStore.getState().getConnectedInputs('dst');
    expect(inputs.videos).toEqual([noExtUrl]);
    expect(inputs.images).toEqual([]);
  });

  it('转白模产物(无扩展名 URL)判为视频——可继续接高清/去字幕', () => {
    seed('video-whitemodel', noExtUrl);
    const inputs = useCanvasStore.getState().getConnectedInputs('dst');
    expect(inputs.videos).toEqual([noExtUrl]);
    expect(inputs.images).toEqual([]);
  });

  it('settings.kind=video 的非 preview 节点也判为视频(兜底,不再只认 preview)', () => {
    seed('gen-image', noExtUrl, { kind: 'video' });
    const inputs = useCanvasStore.getState().getConnectedInputs('dst');
    expect(inputs.videos).toEqual([noExtUrl]);
  });

  it('不回归:gen-image 产物仍判为图片', () => {
    seed('gen-image', 'https://cdn.example.com/gen/task-1-0');
    const inputs = useCanvasStore.getState().getConnectedInputs('dst');
    expect(inputs.images).toHaveLength(1);
    expect(inputs.videos).toEqual([]);
  });

  it('不回归:gen-video / video-input / preview(kind=video) 仍判为视频', () => {
    for (const t of ['gen-video', 'video-input']) {
      seed(t, noExtUrl);
      expect(useCanvasStore.getState().getConnectedInputs('dst').videos).toEqual([noExtUrl]);
    }
    seed('preview', noExtUrl, { kind: 'video' });
    expect(useCanvasStore.getState().getConnectedInputs('dst').videos).toEqual([noExtUrl]);
  });

  it('不回归:音频仍按扩展名/类型判为 audio', () => {
    seed('input-audio', 'https://cdn.example.com/a.mp3');
    const inputs = useCanvasStore.getState().getConnectedInputs('dst');
    expect(inputs.audios).toHaveLength(1);
    expect(inputs.videos).toEqual([]);
  });
});

describe('centerNode（agent 聊天定位 chip 用的视口平移）', () => {
  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [],
      connections: [],
      view: { x: 0, y: 0, zoom: 1 },
      selectedIds: new Set<string>(),
      undoStack: [],
      redoStack: [],
      readOnly: false,
    });
  });

  it('把视口平移到节点中心（保持 zoom），公式与 Minimap 的视口近似一致', () => {
    useCanvasStore.setState({
      nodes: [
        {
          id: 'n-target', type: 'text-node',
          x: 1000, y: 2000, width: 320, height: 360,
          settings: {}, status: 'succeeded',
        },
      ],
      view: { x: 0, y: 0, zoom: 2 },
    });
    useCanvasStore.getState().centerNode('n-target');
    const view = useCanvasStore.getState().view;
    // 节点中心 (1160, 2180)，视口近似 = 窗口减侧栏 56。
    const vpW = window.innerWidth - 56;
    const vpH = window.innerHeight - 56;
    expect(view).toEqual({ x: vpW / 2 - 1160 * 2, y: vpH / 2 - 2180 * 2, zoom: 2 });
  });

  it('节点不存在时是 no-op', () => {
    useCanvasStore.setState({ view: { x: 7, y: 8, zoom: 1.5 } });
    useCanvasStore.getState().centerNode('n-missing');
    expect(useCanvasStore.getState().view).toEqual({ x: 7, y: 8, zoom: 1.5 });
  });
});

// 整拉兜底（团队并发编辑的 version 缺口自愈）走的是同一个 load，但它不是「换画布」
// 而是「把本地对齐到服务端最新」。默认语义清选中 + 清 undo 栈，对这条路径就是纯
// 倒退：用户正开着的参数面板被收起、刚做的几步撤不回去，而这跟「服务端有新数据」
// 毫无关系。
describe('load 的 preserveUiState（整拉兜底用）', () => {
  const node = (id: string) => ({
    id,
    type: 'gen-image' as const,
    x: 0,
    y: 0,
    width: 280,
    height: 280,
    settings: {},
    status: 'idle' as const,
  });

  beforeEach(() => {
    resetStore();
    setCanvasOpSink(null);
  });

  it('默认（换画布）照旧清选中与 undo/redo 栈', () => {
    useCanvasStore.setState({
      selectedIds: new Set(['a']),
      undoStack: [{ nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } }],
      redoStack: [{ nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } }],
    });
    useCanvasStore.getState().load({ nodes: [node('a')], connections: [] });
    const s = useCanvasStore.getState();
    expect([...s.selectedIds]).toEqual([]);
    expect(s.undoStack).toEqual([]);
    expect(s.redoStack).toEqual([]);
  });

  it('preserveUiState 保住选中与 undo/redo 栈', () => {
    useCanvasStore.setState({
      selectedIds: new Set(['a']),
      undoStack: [{ nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } }],
      redoStack: [{ nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } }],
    });
    useCanvasStore.getState().load(
      { nodes: [node('a')], connections: [] },
      { preserveUiState: true },
    );
    const s = useCanvasStore.getState();
    expect([...s.selectedIds]).toEqual(['a']);
    expect(s.undoStack).toHaveLength(1);
    expect(s.redoStack).toHaveLength(1);
  });

  it('preserveUiState 下选中集按新快照剪枝——队友删掉的节点不能还留在选中里', () => {
    useCanvasStore.setState({ selectedIds: new Set(['a', 'gone']) });
    useCanvasStore.getState().load(
      { nodes: [node('a')], connections: [] },
      { preserveUiState: true },
    );
    expect([...useCanvasStore.getState().selectedIds]).toEqual(['a']);
  });
});

describe('load 的 resetHistory（外部变更整拉用）', () => {
  it('clears undo history and dangling ui ids on external reload', () => {
    const a = { id: 'a', type: 'text', x: 0, y: 0 } as unknown as CanvasNode;
    const b = { id: 'b', type: 'text', x: 0, y: 0 } as unknown as CanvasNode;
    useCanvasStore.setState({
      nodes: [a, b],
      selectedIds: new Set(['a', 'b']),
      undoStack: [{ nodes: [a], connections: [] } as unknown as CanvasSnapshot],
      redoStack: [{ nodes: [], connections: [] } as unknown as CanvasSnapshot],
      croppingNodeId: 'a',
      dockPanelNodeId: 'b',
      audioEdit: { nodeId: 'a', mode: 'trim', start: 0, end: 1 } as unknown as AudioEditState,
      view: { x: 10, y: 20, zoom: 1.5 },
    });
    useCanvasStore.getState().load(
      { nodes: [b], connections: [], view: { x: 10, y: 20, zoom: 1.5 } },
      { preserveUiState: true, resetHistory: true },
    );
    const s = useCanvasStore.getState();
    expect([...s.selectedIds]).toEqual(['b']);
    expect(s.undoStack).toEqual([]);
    expect(s.redoStack).toEqual([]);
    expect(s.croppingNodeId).toBeNull();
    expect(s.dockPanelNodeId).toBe('b');
    expect(s.audioEdit).toBeNull();
    expect(s.view).toEqual({ x: 10, y: 20, zoom: 1.5 });
  });

  it('preserveUiState without resetHistory keeps undo stack (existing behavior)', () => {
    const a = { id: 'a', type: 'text', x: 0, y: 0 } as unknown as CanvasNode;
    useCanvasStore.setState({ nodes: [a], undoStack: [{ nodes: [], connections: [] } as unknown as CanvasSnapshot] });
    useCanvasStore.getState().load({ nodes: [a], connections: [] }, { preserveUiState: true });
    expect(useCanvasStore.getState().undoStack).toHaveLength(1);
  });
});
