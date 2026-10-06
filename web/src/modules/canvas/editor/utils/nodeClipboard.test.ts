import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useCanvasStore } from '../store';
import type { CanvasNode } from '../types';
import { buildPayload, collectMediaKeys, parsePayload } from './nodeClipboard';

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    api: { post: vi.fn() },
    currentProjectId: vi.fn().mockReturnValue(7),
  };
});

function node(id: string, patch?: Partial<CanvasNode>): CanvasNode {
  return {
    id,
    type: 'input-image',
    x: 0,
    y: 0,
    width: 260,
    height: 260,
    status: 'idle',
    settings: {},
    ...patch,
  };
}

function resetStore(nodes: CanvasNode[], connections: Array<{ id: string; from: string; to: string }> = []) {
  useCanvasStore.setState({
    nodes,
    connections,
    selectedIds: new Set<string>(),
    view: { x: 0, y: 0, zoom: 1 },
    undoStack: [],
    redoStack: [],
    readOnly: false,
  });
}

describe('nodeClipboard（复制节点的剪贴板层）', () => {
  beforeEach(() => {
    localStorage.clear();
    resetStore([]);
  });

  it('buildPayload 只带批内连线，且净化运行时字段（taskId/pendingGen/瞬态 status）', () => {
    resetStore(
      [
        node('n-a', { taskId: 42, status: 'generating', content: 'media://k/a.png', settings: { pendingGen: true, prompt: 'x' } }),
        node('n-b'),
        node('n-out'),
      ],
      [
        { id: 'e-1', from: 'n-a', to: 'n-b' },
        // 出边：另一端不在复制集里，不能带走。
        { id: 'e-2', from: 'n-b', to: 'n-out' },
      ],
    );
    const payload = buildPayload(['n-a', 'n-b'])!;
    expect(payload.sourceProjectId).toBe(7);
    expect(payload.nodes).toHaveLength(2);
    expect(payload.connections).toHaveLength(1);
    expect(payload.connections[0]).toMatchObject({ from: 'n-a', to: 'n-b' });
    const a = payload.nodes.find((n) => n.id === 'n-a')!;
    expect(a.taskId).toBeUndefined();
    expect(a.settings!.pendingGen).toBeUndefined();
    // 有内容的 generating 收敛成 succeeded——粘出来的节点不能永远转圈。
    expect(a.status).toBe('succeeded');
  });

  it('buildPayload 空选择返回 null', () => {
    expect(buildPayload(['no-such'])).toBeNull();
  });

  it('parsePayload 拒绝非本格式/形状不对的数据（剪贴板内容不可信）', () => {
    expect(parsePayload('not json')).toBeNull();
    expect(parsePayload(JSON.stringify({ format: 'other' }))).toBeNull();
    expect(
      parsePayload(JSON.stringify({ format: 'tvmao-canvas-nodes', version: 1, nodes: [{ id: 1 }], connections: [] })),
    ).toBeNull();
    const ok = JSON.stringify({
      format: 'tvmao-canvas-nodes',
      version: 1,
      sourceProjectId: 7,
      nodes: [node('n-a')],
      connections: [],
    });
    expect(parsePayload(ok)).not.toBeNull();
  });

  it('collectMediaKeys 深扫 content 与 settings（含 history 数组），去重', () => {
    const payload = {
      format: 'tvmao-canvas-nodes' as const,
      version: 1 as const,
      sourceProjectId: 7,
      nodes: [
        node('n-a', {
          content: 'media://proj/1.png',
          settings: {
            history: [{ url: 'media://proj/old.png' }, { url: 'media://proj/1.png' }],
            prompt: '不含引用',
          },
        }),
        node('n-b', { content: 'https://external.example/x.png' }),
      ],
      connections: [],
    };
    const keys = collectMediaKeys(payload as never);
    expect(keys.sort()).toEqual(['proj/1.png', 'proj/old.png']);
  });
});

describe('store.pasteNodes（粘贴落地）', () => {
  beforeEach(() => resetStore([]));

  it('重发 id、重建批内连线、@ 提及跟着改指副本', () => {
    const nodes = [
      node('n-src', { content: 'media://k/s.png' }),
      node('n-gen', { type: 'gen-image', x: 400, settings: { prompt: '用 @[图片:n-src] 生成' } }),
    ];
    const conns = [{ id: 'e-1', from: 'n-src', to: 'n-gen' }];
    const ids = useCanvasStore.getState().pasteNodes(nodes, conns, { x: 1000, y: 1000 });
    expect(ids).toHaveLength(2);

    const s = useCanvasStore.getState();
    expect(s.nodes).toHaveLength(2);
    // id 全新
    expect(s.nodes.map((n) => n.id).sort()).toEqual([...ids].sort());
    expect(s.nodes.find((n) => n.id === 'n-src')).toBeUndefined();
    // 连线两端映射到副本
    expect(s.connections).toHaveLength(1);
    const [c] = s.connections;
    expect(ids).toContain(c.from);
    expect(ids).toContain(c.to);
    // 提及重写
    const gen = s.nodes.find((n) => n.type === 'gen-image')!;
    expect(gen.settings!.prompt).toBe(`用 @[图片:${c.from}] 生成`);
    // 粘贴后成为选中集，且可撤销
    expect([...s.selectedIds].sort()).toEqual([...ids].sort());
    expect(s.undoStack.length).toBe(1);
  });

  it('包围盒中心对齐到落点，相对位置保持', () => {
    const nodes = [node('n-a', { x: 0, y: 0 }), node('n-b', { x: 400, y: 0 })];
    useCanvasStore.getState().pasteNodes(nodes, [], { x: 500, y: 500 });
    const s = useCanvasStore.getState();
    const xs = s.nodes.map((n) => n.x).sort((a, b) => a - b);
    // bbox 宽 660(0..660)，中心对齐 500 → minX = 500-330 = 170
    expect(xs[0]).toBe(170);
    expect(xs[1] - xs[0]).toBe(400); // 相对位置不变
  });

  it('同一份 payload 粘两次互不污染（settings 深拷贝）', () => {
    const nodes = [node('n-a', { settings: { prompt: '看 @[图片:n-a]' } })];
    const first = useCanvasStore.getState().pasteNodes(nodes, [], { x: 0, y: 0 });
    const second = useCanvasStore.getState().pasteNodes(nodes, [], { x: 100, y: 100 });
    const s = useCanvasStore.getState();
    const p1 = s.nodes.find((n) => n.id === first[0])!.settings!.prompt as string;
    const p2 = s.nodes.find((n) => n.id === second[0])!.settings!.prompt as string;
    // 各自指向各自的新 id——如果浅拷贝共享 settings，第二次粘贴会把第一次的也改掉。
    expect(p1).toContain(first[0]);
    expect(p2).toContain(second[0]);
    expect(p1).not.toBe(p2);
  });

  it('只读模式不落任何东西', () => {
    useCanvasStore.setState({ readOnly: true });
    const ids = useCanvasStore.getState().pasteNodes([node('n-a')], [], { x: 0, y: 0 });
    expect(ids).toEqual([]);
    expect(useCanvasStore.getState().nodes).toHaveLength(0);
  });
});
