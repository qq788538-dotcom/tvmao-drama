import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';

// 3D 依赖不进 jsdom：desk App 换成轻量占位（同 desk 自己 App.test 对
// DirectorCanvas 的做法）。本文件测的是画布集成层，不是 3D 内部。
vi.mock('./desk/App', () => ({
  default: () => <div data-testid="mock-desk-app" />,
}));

// 持久化层 mock 掉：persistNodeById/scheduleNodePatch 在 jsdom 里会打真 HTTP。
// NodeShell 也 import 本模块，故必须把它用到的导出一并提供。
vi.mock('../../persistence/canvasMutations', () => ({
  enqueueCanvasMutation: vi.fn(),
  flushNodePatch: vi.fn(async () => {}),
  geometryPatchFromNode: vi.fn(() => ({})),
  getCanvasOwnerToken: vi.fn(() => null),
  scheduleNodePatch: vi.fn(),
  persistNodeById: vi.fn(),
}));

import { DirectorDeskNode, resolveCachedAssetUrl } from './DirectorDeskNode';
import { useCanvasStore } from '../../store';
import { persistNodeById, scheduleNodePatch } from '../../persistence/canvasMutations';
import { setDirectorHostAdapter } from './desk/editor/io/hostAdapter';

beforeEach(() => {
  setDirectorHostAdapter(null);
  useCanvasStore.setState({
    nodes: [{ id: 'dd1', type: 'director-desk', x: 0, y: 0, width: 360, height: 240 }],
    connections: [],
    selectedIds: new Set(),
  });
  vi.mocked(persistNodeById).mockClear();
  vi.mocked(scheduleNodePatch).mockClear();
});

// window.postMessage 真派发是异步任务；测试里直接合成 MessageEvent 同步派发，
// origin 需与组件校验一致。
function dispatchDeskMessage(data: unknown) {
  act(() => {
    fireEvent(window, new MessageEvent('message', { data, origin: window.location.origin }));
  });
}

// 对照组：卡片本体右键必须有 NodeShell 兜底「删除」菜单——它证明本测试环境里
// 兜底菜单真的会渲染，防止下面的否定断言假绿。
it('节点卡片本体右键仍出画布兜底「删除」菜单', () => {
  render(<DirectorDeskNode id="dd1" />);
  fireEvent.contextMenu(screen.getByTestId('director-desk-open-panel'));
  expect(screen.getByText('删除')).toBeInTheDocument();
});

// 回归：createPortal 只改 DOM 挂载点，React 合成事件仍沿组件树冒泡回 NodeShell。
// 修复前在全屏导演台里右键会弹出画布节点的「删除」菜单（线上实拍）；包裹层
// stopPropagation 后不再穿透。
it('全屏面板内右键不再穿透成画布节点「删除」菜单', async () => {
  render(<DirectorDeskNode id="dd1" />);
  fireEvent.click(screen.getByTestId('director-desk-open-panel'));
  const desk = await screen.findByTestId('mock-desk-app');

  fireEvent.contextMenu(desk);
  expect(screen.queryByText('删除')).not.toBeInTheDocument();

  // 点击/滚轮同理不该穿透成节点选中或画布缩放——至少保证不抛错且不冒出菜单。
  fireEvent.mouseDown(desk);
  fireEvent.click(desk);
  fireEvent.wheel(desk);
  expect(screen.queryByText('删除')).not.toBeInTheDocument();
});

// 阶段二：captures-sent（载荷已是签名 URL）→ 在导演台右侧铺 input-image 节点、
// 写 content/name、persist、并从导演台连线过去。
it('captures-sent 铺 input-image 节点并连线', async () => {
  render(<DirectorDeskNode id="dd1" />);
  fireEvent.click(screen.getByTestId('director-desk-open-panel'));
  await screen.findByTestId('mock-desk-app');

  dispatchDeskMessage({
    type: 'storyai:director-desk-captures-sent',
    payload: {
      captures: [
        { dataUrl: 'https://media.example/u1/2026/08/cap-a.png?X-Sig=1', fileName: '机位1-截图01.png' },
        { dataUrl: 'https://media.example/u1/2026/08/cap-b.png?X-Sig=2', fileName: '机位1-截图02.png' },
      ],
    },
  });

  await waitFor(() => {
    const state = useCanvasStore.getState();
    const spawned = state.nodes.filter((n) => n.type === 'input-image');
    expect(spawned).toHaveLength(2);
    expect(spawned[0].content).toBe('https://media.example/u1/2026/08/cap-a.png?X-Sig=1');
    expect(spawned[0].settings?.name).toBe('机位1-截图01');
    // 两张不许叠在同一坐标（layoutDropPoints 排布）。
    expect(spawned[0].x).not.toBe(spawned[1].x);
    // 导演台 → 新节点各有一条连线。
    expect(state.connections.filter((c) => c.from === 'dd1')).toHaveLength(2);
  });
  // 内容补齐必须显式 persist（addNode 广播的 upsert 里还没有 content）。
  expect(persistNodeById).toHaveBeenCalledTimes(2);
});

// dataURL 禁入库硬规则：上传失败回落的 data: 截图发送时必须整批拒收，
// 一个节点都不建。
it('captures-sent 载荷是 dataURL 时拒收不建节点', async () => {
  render(<DirectorDeskNode id="dd1" />);
  fireEvent.click(screen.getByTestId('director-desk-open-panel'));
  await screen.findByTestId('mock-desk-app');

  dispatchDeskMessage({
    type: 'storyai:director-desk-captures-sent',
    payload: { captures: [{ dataUrl: 'data:image/png;base64,AAAA', fileName: 'x.png' }] },
  });

  expect(useCanvasStore.getState().nodes.filter((n) => n.type === 'input-image')).toHaveLength(0);
  expect(persistNodeById).not.toHaveBeenCalled();
});

// 阶段二持久化链路：desk 写状态 → adapter → scheduleNodePatch 落到本节点
// settings.directorProject（debounce/收敛 media:// 由画布链路负责，这里只验接线）。
it('打开面板注册 adapter，writeState 走 scheduleNodePatch 写 settings.directorProject', async () => {
  const { getDirectorHostAdapter } = await import('./desk/editor/io/hostAdapter');
  render(<DirectorDeskNode id="dd1" />);
  fireEvent.click(screen.getByTestId('director-desk-open-panel'));
  await screen.findByTestId('mock-desk-app');

  const adapter = getDirectorHostAdapter();
  expect(adapter).not.toBeNull();
  adapter!.writeState({ version: 1, objects: [] });
  expect(scheduleNodePatch).toHaveBeenCalledWith('dd1', {
    settings: { directorProject: { version: 1, objects: [] } },
  });
});

// 回归：每次打开导演台都 createObjectURL 一个新地址，R3F useLoader 按 URL 缓存
// 永远命不中（整只模型重新解析），旧 blob 也永不回收。同一资产（去 query 后）
// 整个会话只能造一个 blob URL；失败回落直连 URL 且不缓存失败结果。
describe('resolveCachedAssetUrl', () => {
  const origCaches = (globalThis as { caches?: unknown }).caches;
  const origFetch = globalThis.fetch;
  const origCreate = URL.createObjectURL;

  afterEach(() => {
    (globalThis as { caches?: unknown }).caches = origCaches;
    globalThis.fetch = origFetch;
    URL.createObjectURL = origCreate;
  });

  it('同一资产不同签名 query 复用同一个 blob URL，只下载一次', async () => {
    const store = new Map<string, Response>();
    (globalThis as { caches?: unknown }).caches = {
      open: vi.fn(async () => ({
        match: vi.fn(async (k: string) => store.get(k)?.clone()),
        put: vi.fn(async (k: string, r: Response) => void store.set(k, r)),
      })),
    };
    const fetchMock = vi.fn(async () => new Response(new Blob(['m']), { status: 200 }));
    globalThis.fetch = fetchMock as unknown as typeof fetch;
    let n = 0;
    const createMock = vi.fn(() => `blob:mock-${++n}`);
    URL.createObjectURL = createMock;

    const a = await resolveCachedAssetUrl('https://oss.example/m/reuse.glb?sig=1');
    const b = await resolveCachedAssetUrl('https://oss.example/m/reuse.glb?sig=2');
    expect(a).toBe('blob:mock-1');
    expect(b).toBe(a);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(createMock).toHaveBeenCalledTimes(1);
  });

  it('下载失败回落原 URL，且下次重试', async () => {
    (globalThis as { caches?: unknown }).caches = {
      open: vi.fn(async () => ({ match: vi.fn(async () => undefined), put: vi.fn(async () => {}) })),
    };
    const fetchMock = vi.fn(async () => new Response('', { status: 500 }));
    globalThis.fetch = fetchMock as unknown as typeof fetch;
    const url = 'https://oss.example/m/fail.glb?sig=1';
    expect(await resolveCachedAssetUrl(url)).toBe(url);
    expect(await resolveCachedAssetUrl(url)).toBe(url);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
