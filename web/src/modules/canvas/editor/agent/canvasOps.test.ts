// applyCanvasOpArtifact 的行为测试：跑在真实 zustand store 上（与 store.test.ts
// 同一套路），断言的是画布状态的最终形态而不是 mock 调用序列——这些操作的
// 验收点是「与用户手动操作语义一致」（undo 入栈、连带清连线、去重），
// 只 mock store 的话这些语义全都测不到。持久化入口（persistNode）保持注入
// mock：真实现会拉 canvasMutations→api 链，与本测试无关。

import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { useCanvasStore } from '../store';
import type { CanvasNode } from '../types';
import {
  applyCanvasOpArtifact,
  isCanvasOpArtifact,
  parseCanvasOpPayload,
} from './canvasOps';

const agentMeta = { threadId: 't1', actionId: 'a1', artifactId: 'art-1', agentName: 'planner' };

function makeNode(id: string, extra: Partial<CanvasNode> = {}): CanvasNode {
  return {
    id,
    type: 'gen-image',
    x: 0,
    y: 0,
    width: 280,
    height: 280,
    settings: {},
    status: 'idle',
    ...extra,
  };
}

function resetStore(partial: Partial<{ nodes: CanvasNode[]; connections: { id: string; from: string; to: string; inputType?: string }[] }> = {}) {
  useCanvasStore.setState({
    nodes: [],
    connections: [],
    view: { x: 0, y: 0, zoom: 1 },
    selectedIds: new Set<string>(),
    undoStack: [],
    redoStack: [],
    readOnly: false,
    lightbox: null,
    connecting: null,
    ...partial,
  });
}

// 一个配了 uiSchema 的模型目录：切到它要把比例/时长重置成它的默认值。
const UI_SCHEMAS: Record<string, Record<string, { options: Array<{ value: string | number; label: string }>; default?: string | number }>> = {
  'model-a': {
    ratio: { default: '16:9', options: [{ value: '16:9', label: '横' }, { value: '9:16', label: '竖' }] },
    duration: { default: 5, options: [{ value: 5, label: '5s' }, { value: 10, label: '10s' }] },
  },
  'model-b': {
    ratio: { default: '1:1', options: [{ value: '1:1', label: '方' }] },
  },
};

function apply(
  artifact: { artifact_type?: string; artifact_id: string; name?: string; payload?: unknown },
  opts: { withModels?: boolean } = {},
) {
  const persistNode = vi.fn();
  applyCanvasOpArtifact({
    store: useCanvasStore.getState(),
    artifact,
    agentMeta,
    nextDropPoint: () => ({ x: 1000, y: 2000 }),
    persistNode,
    resolveUiSchema: opts.withModels ? (id) => UI_SCHEMAS[id] : undefined,
  });
  return persistNode;
}

let warnSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  resetStore();
  warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
});

afterEach(() => {
  warnSpy.mockRestore();
});

describe('isCanvasOpArtifact 分流守卫', () => {
  it('五种画布操作类型都放行，其余类型与 undefined 都拒绝', () => {
    for (const t of [
      'node_create_info',
      'node_update_info',
      'node_delete_info',
      'connection_create_info',
      'connection_delete_info',
    ]) {
      expect(isCanvasOpArtifact(t)).toBe(true);
    }
    expect(isCanvasOpArtifact('text_info')).toBe(false);
    expect(isCanvasOpArtifact('image_info')).toBe(false);
    expect(isCanvasOpArtifact(undefined)).toBe(false);
  });
});

describe('parseCanvasOpPayload', () => {
  it('内嵌 JSON 对象（后端 json.RawMessage 的真实形态）直接返回', () => {
    expect(parseCanvasOpPayload({ node_id: 'n-1' })).toEqual({ node_id: 'n-1' });
  });

  it('字符串化 JSON 是防御分支，也能解出来；坏字符串/数组/缺失返回 null', () => {
    expect(parseCanvasOpPayload('{"from":"a","to":"b"}')).toEqual({ from: 'a', to: 'b' });
    expect(parseCanvasOpPayload('not json')).toBeNull();
    expect(parseCanvasOpPayload([1, 2])).toBeNull();
    expect(parseCanvasOpPayload(undefined)).toBeNull();
  });
});

describe('node_create_info', () => {
  it('带显式坐标：按左上角语义落节点，settings 合并 + agentMeta 溯源，机器字段被剥', () => {
    const persistNode = apply({
      artifact_type: 'node_create_info',
      artifact_id: 'art-1',
      payload: {
        type: 'gen-image',
        // status/agentMeta 是机器管理字段：后端已剥，这里验证前端纵深防御。
        settings: { prompt: '雨夜', name: '镜头1', status: 'generating', agentMeta: { artifactId: 'evil' } },
        x: 40,
        y: 60,
      },
    });

    const nodes = useCanvasStore.getState().nodes;
    expect(nodes).toHaveLength(1);
    const node = nodes[0];
    // read_canvas 回显的是左上角坐标，创建也必须按左上角执行（addNode 内部是中心点语义）。
    expect(node.x).toBe(40);
    expect(node.y).toBe(60);
    expect(node.settings?.prompt).toBe('雨夜');
    expect(node.settings?.name).toBe('镜头1');
    // 不改 status：铺参数和点火永远是两个动作。
    expect(node.status).toBe('idle');
    // 溯源信息是我方生成的，payload 里混进来的同名字段不生效。
    expect((node.settings?.agentMeta as { artifactId?: string }).artifactId).toBe('art-1');
    expect(persistNode).toHaveBeenCalledWith(node.id);
  });

  it('不带坐标：复用 agentDropPoint 排布（nextDropPoint 是中心点语义）', () => {
    apply({
      artifact_type: 'node_create_info',
      artifact_id: 'art-1',
      payload: { type: 'gen-image', settings: {} },
    });
    const node = useCanvasStore.getState().nodes[0];
    // addNode 把落点当中心：x = 1000 - 280/2。
    expect(node.x).toBe(1000 - 280 / 2);
    expect(node.y).toBe(2000 - 280 / 2);
  });

  it('未知节点类型：warn + 丢弃，不落节点', () => {
    const persistNode = apply({
      artifact_type: 'node_create_info',
      artifact_id: 'art-1',
      payload: { type: 'not-a-node-type', settings: {} },
    });
    expect(useCanvasStore.getState().nodes).toHaveLength(0);
    expect(persistNode).not.toHaveBeenCalled();
    expect(warnSpy).toHaveBeenCalled();
  });
});

describe('node_update_info', () => {
  it('settings 合并 + 几何改动一并生效，undo 入栈可一步回退，persist 一次', () => {
    resetStore({ nodes: [makeNode('n-1', { settings: { prompt: '旧', keep: 'me' }, status: 'succeeded' })] });
    const persistNode = apply({
      artifact_type: 'node_update_info',
      artifact_id: 'art-1',
      payload: { node_id: 'n-1', settings: { prompt: '新' }, x: 5, width: 999, y: null, height: null },
    });

    const node = useCanvasStore.getState().nodes[0];
    expect(node.settings?.prompt).toBe('新');
    // 合并而不是整体替换：未提及的字段原样保留。
    expect(node.settings?.keep).toBe('me');
    expect(node.x).toBe(5);
    expect(node.width).toBe(999);
    // null（后端未填的指针字段）不碰对应几何。
    expect(node.y).toBe(0);
    expect(node.height).toBe(280);
    // 不碰机器管理的 status。
    expect(node.status).toBe('succeeded');
    expect(persistNode).toHaveBeenCalledWith('n-1');
    // 显式 commit 了一次快照：Ctrl+Z 能一步回退 agent 的整个修改。
    expect(useCanvasStore.getState().undoStack).toHaveLength(1);
    useCanvasStore.getState().undo();
    expect(useCanvasStore.getState().nodes[0].settings?.prompt).toBe('旧');
  });

  it('目标节点不存在：warn + 丢弃，状态零变化', () => {
    resetStore({ nodes: [makeNode('n-1')] });
    const persistNode = apply({
      artifact_type: 'node_update_info',
      artifact_id: 'art-1',
      payload: { node_id: 'n-ghost', settings: { prompt: 'x' } },
    });
    expect(useCanvasStore.getState().nodes[0].settings?.prompt).toBeUndefined();
    expect(useCanvasStore.getState().undoStack).toHaveLength(0);
    expect(persistNode).not.toHaveBeenCalled();
    expect(warnSpy).toHaveBeenCalled();
  });
});

describe('node_delete_info', () => {
  it('走 store.deleteNodes：节点删除 + 连带清相关连线 + undo 入栈', () => {
    resetStore({
      nodes: [makeNode('n-1'), makeNode('n-2')],
      connections: [{ id: 'e-1', from: 'n-1', to: 'n-2' }],
    });
    apply({
      artifact_type: 'node_delete_info',
      artifact_id: 'art-1',
      payload: { node_id: 'n-1' },
    });
    const s = useCanvasStore.getState();
    expect(s.nodes.map((n) => n.id)).toEqual(['n-2']);
    expect(s.connections).toHaveLength(0);
    expect(s.undoStack).toHaveLength(1);
  });

  it('目标节点不存在：warn + 丢弃（也不产生空 undo 快照）', () => {
    resetStore({ nodes: [makeNode('n-1')] });
    apply({
      artifact_type: 'node_delete_info',
      artifact_id: 'art-1',
      payload: { node_id: 'n-ghost' },
    });
    expect(useCanvasStore.getState().nodes).toHaveLength(1);
    expect(useCanvasStore.getState().undoStack).toHaveLength(0);
    expect(warnSpy).toHaveBeenCalled();
  });
});

describe('connection_create_info', () => {
  it('两端节点都在：走 store.addConnection 建线', () => {
    resetStore({ nodes: [makeNode('n-1'), makeNode('n-2')] });
    apply({
      artifact_type: 'connection_create_info',
      artifact_id: 'art-1',
      payload: { from: 'n-1', to: 'n-2' },
    });
    const conns = useCanvasStore.getState().connections;
    expect(conns).toHaveLength(1);
    expect(conns[0]).toMatchObject({ from: 'n-1', to: 'n-2' });
  });

  it('任一端不存在：warn + 丢弃，不留悬空连线', () => {
    resetStore({ nodes: [makeNode('n-1')] });
    apply({
      artifact_type: 'connection_create_info',
      artifact_id: 'art-1',
      payload: { from: 'n-1', to: 'n-ghost' },
    });
    expect(useCanvasStore.getState().connections).toHaveLength(0);
    expect(warnSpy).toHaveBeenCalled();
  });
});

describe('connection_delete_info', () => {
  it('按 from+to 找到连线并移除（同对节点多条 inputType 变体一并断开）', () => {
    resetStore({
      nodes: [makeNode('n-1'), makeNode('n-2'), makeNode('n-3')],
      connections: [
        { id: 'e-1', from: 'n-1', to: 'n-2' },
        { id: 'e-2', from: 'n-1', to: 'n-2', inputType: 'ref' },
        { id: 'e-3', from: 'n-1', to: 'n-3' },
      ],
    });
    apply({
      artifact_type: 'connection_delete_info',
      artifact_id: 'art-1',
      payload: { from: 'n-1', to: 'n-2' },
    });
    expect(useCanvasStore.getState().connections.map((c) => c.id)).toEqual(['e-3']);
  });

  it('连线不存在：warn + 丢弃', () => {
    resetStore({ nodes: [makeNode('n-1'), makeNode('n-2')] });
    apply({
      artifact_type: 'connection_delete_info',
      artifact_id: 'art-1',
      payload: { from: 'n-1', to: 'n-2' },
    });
    expect(warnSpy).toHaveBeenCalled();
  });
});

describe('坏 payload', () => {
  it('payload 缺失或非法：warn + 丢弃，状态零变化', () => {
    resetStore({ nodes: [makeNode('n-1')] });
    const persistNode = apply({ artifact_type: 'node_update_info', artifact_id: 'art-1' });
    expect(useCanvasStore.getState().nodes).toHaveLength(1);
    expect(persistNode).not.toHaveBeenCalled();
    expect(warnSpy).toHaveBeenCalled();
  });
});

// ---- 能力清单：字段权限与切模型联动 ----

describe('按能力清单剥字段', () => {
  beforeEach(() => resetStore());

  it('只读字段被剥掉，open 字段留下', () => {
    resetStore({ nodes: [makeNode('n1', { type: 'script', settings: { rows: [] } })] });
    apply({
      artifact_type: 'node_update_info',
      artifact_id: 'a',
      payload: { node_id: 'n1', settings: { scriptPrompt: '写十场戏', durationSec: 999 } },
    });
    const s = useCanvasStore.getState().nodes[0].settings as Record<string, unknown>;
    expect(s.scriptPrompt).toBe('写十场戏');
    // durationSec 是分镜时长的上界，改它会让已有行越界——清单里是 readonly。
    expect(s.durationSec).toBeUndefined();
  });

  it('机器字段被剥掉——直写会伪造「生成中」的假 UI 状态', () => {
    resetStore({ nodes: [makeNode('n1', { type: 'gen-video' })] });
    apply({
      artifact_type: 'node_update_info',
      artifact_id: 'a',
      payload: { node_id: 'n1', settings: { videoPrompt: 'ok', status: 'generating', history: [1] } },
    });
    const s = useCanvasStore.getState().nodes[0].settings as Record<string, unknown>;
    expect(s.videoPrompt).toBe('ok');
    expect(s.status).toBeUndefined();
    expect(s.history).toBeUndefined();
  });

  it('动态字段（ratio/duration）放行——它们的合法性后端按模型 uiSchema 判过了', () => {
    resetStore({ nodes: [makeNode('n1', { type: 'gen-video' })] });
    apply({
      artifact_type: 'node_update_info',
      artifact_id: 'a',
      payload: { node_id: 'n1', settings: { ratio: '9:16' } },
    });
    expect((useCanvasStore.getState().nodes[0].settings as Record<string, unknown>).ratio).toBe('9:16');
  });
});

describe('切模型时重置动态字段', () => {
  beforeEach(() => resetStore());

  it('换模型把比例/时长重置成新模型默认值，不留旧模型的残留', () => {
    resetStore({
      nodes: [makeNode('n1', { type: 'gen-video', settings: { modelId: 'model-a', ratio: '9:16', duration: 10 } })],
    });
    apply(
      { artifact_type: 'node_update_info', artifact_id: 'a', payload: { node_id: 'n1', settings: { modelId: 'model-b' } } },
      { withModels: true },
    );
    const s = useCanvasStore.getState().nodes[0].settings as Record<string, unknown>;
    expect(s.modelId).toBe('model-b');
    // model-b 只有 ratio，默认 1:1——旧模型的 9:16 必须被顶掉。
    expect(s.ratio).toBe('1:1');
    // duration 是 model-a 才有的字段；model-b 的默认值里没有它，旧值原样留着
    // 也无妨（前端读参数时按新模型的 uiSchema 走，读不到就用不上）。
    expect(s.duration).toBe(10);
  });

  it('agent 同时显式给了比例，它的值赢过默认值', () => {
    resetStore({ nodes: [makeNode('n1', { type: 'gen-video', settings: { modelId: 'model-b' } })] });
    apply(
      {
        artifact_type: 'node_update_info',
        artifact_id: 'a',
        payload: { node_id: 'n1', settings: { modelId: 'model-a', ratio: '9:16' } },
      },
      { withModels: true },
    );
    const s = useCanvasStore.getState().nodes[0].settings as Record<string, unknown>;
    expect(s.ratio).toBe('9:16');
    expect(s.duration).toBe(5);
  });

  it('同值重写不重置——不该把用户手调过的分辨率打回默认', () => {
    resetStore({
      nodes: [makeNode('n1', { type: 'gen-video', settings: { modelId: 'model-a', ratio: '9:16' } })],
    });
    apply(
      { artifact_type: 'node_update_info', artifact_id: 'a', payload: { node_id: 'n1', settings: { modelId: 'model-a' } } },
      { withModels: true },
    );
    expect((useCanvasStore.getState().nodes[0].settings as Record<string, unknown>).ratio).toBe('9:16');
  });

  it('没有模型目录时退化成只写 modelId，不因此失败', () => {
    resetStore({ nodes: [makeNode('n1', { type: 'gen-video', settings: { modelId: 'model-a' } })] });
    apply({ artifact_type: 'node_update_info', artifact_id: 'a', payload: { node_id: 'n1', settings: { modelId: 'model-b' } } });
    expect((useCanvasStore.getState().nodes[0].settings as Record<string, unknown>).modelId).toBe('model-b');
  });

  it('新建 gen 节点带 modelId 时一并补上该模型的默认参数', () => {
    apply(
      {
        artifact_type: 'node_create_info',
        artifact_id: 'a',
        payload: { type: 'gen-video', settings: { modelId: 'model-a', videoPrompt: 'p' } },
      },
      { withModels: true },
    );
    const s = useCanvasStore.getState().nodes[0].settings as Record<string, unknown>;
    expect(s.modelId).toBe('model-a');
    expect(s.ratio).toBe('16:9');
    expect(s.duration).toBe(5);
  });
});
