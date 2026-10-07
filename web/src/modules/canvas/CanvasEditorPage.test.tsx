import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { AxiosHeaders, type AxiosError, type InternalAxiosRequestConfig } from 'axios';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CanvasEditorPage, LOCAL_OWNER_TOKEN } from './CanvasEditorPage';
import { resetSidecarSessionForTest, setSidecarSession } from '@/shared/sidecarSession';
import { loadLastProjectId } from '@/desktop/lastProject';

const messageError = vi.fn();
const loadMock = vi.fn();

const getSnapshot = vi.fn();
const getProject = vi.fn();
const updateProject = vi.fn();

// 桌面端页面自己不建 WebSocket（没有观看模式、没有团队实时通道）；构造器一旦被
// 调用，就说明还残留着实时通道代码。
const agentClientCtor = vi.fn();
import { useProjectsModal } from '@/modules/canvas/projectsModalStore';
import { useSettingsModal } from '@/modules/settings/settingsModalStore';
vi.mock('@/modules/canvas/editor/agent/agentClient', () => ({
  AgentClient: class {
    constructor(opts: unknown) { agentClientCtor(opts); }
    onFrame() {}
    onOpen() {}
    onClose() {}
    connect() {}
    close() {}
  },
}));

const configureCanvasPersistence = vi.fn();
const setCanvasOwnerToken = vi.fn();
const flushCanvasMutations = vi.fn(async () => undefined);
const resetCanvasPersistence = vi.fn();
const retireCanvasPersistence = vi.fn();
const waitForCanvasMutationsIdle = vi.fn(async () => undefined);
const seedCanvasBaselines = vi.fn();
const loadLocalView = vi.fn(() => ({ x: 7, y: 8, zoom: 1.2 }));
// 真实 antd 的 App.useApp() 每次调用返回同一个 message API 对象（走 context，
// 引用稳定）。这里必须复刻这个稳定性：页面的快照 effect 把 message 放进依赖数组，
// mock 每次渲染都 new 一个新对象的话，任何无关的 state 更新都会让它重跑。
const messageApi = { error: messageError, success: vi.fn() };

vi.mock('antd', async () => {
  const actual = await vi.importActual<typeof import('antd')>('antd');
  return {
    ...actual,
    App: Object.assign(
      ({ children }: { children: React.ReactNode }) => <>{children}</>,
      { useApp: () => ({ message: messageApi }) },
    ),
  };
});

vi.mock('@/modules/canvas/editor/CanvasEditor', () => ({
  CanvasEditor: ({ projectId }: { projectId: number }) => <div data-testid="canvas-editor">editor-{projectId}</div>,
}));

vi.mock('@/modules/canvas/editor/Sidebar', () => ({
  Sidebar: () => <div>sidebar</div>,
}));

vi.mock('@/modules/canvas/editor/Lightbox', () => ({
  Lightbox: () => null,
}));

vi.mock('@/modules/canvas/editor/Minimap', () => ({
  // 用可见标记代替 null，这样能断言故事板模式下它被门控隐藏了
  // （回归②：Minimap 曾漏了 view === 'canvas' 门控，浮在故事板内容之上）。
  Minimap: () => <div data-testid="minimap" />,
}));

vi.mock('@/modules/canvas/editor/store', () => {
  // nodes:[] 与真实 store 的初始值一致（永远不是 undefined）。
  const state = () => ({ load: loadMock, nodes: [], view: { x: 0, y: 0, zoom: 1 } });
  return {
    useCanvasStore: Object.assign(
      (selector: (s: ReturnType<typeof state>) => unknown) => selector(state()),
      { getState: () => state() },
    ),
  };
});

vi.mock('@/modules/canvas/editor/persistence/canvasMutations', () => ({
  // owner 失权提示的订阅（M2），本文件不关心；失权提示的测试在 CanvasEditorPage.errors.test.tsx。
  onCanvasOwnerLost: () => () => undefined,
  configureCanvasPersistence: (...args: Parameters<typeof configureCanvasPersistence>) => configureCanvasPersistence(...args),
  setCanvasOwnerToken: (...args: Parameters<typeof setCanvasOwnerToken>) => setCanvasOwnerToken(...args),
  flushCanvasMutations: (...args: Parameters<typeof flushCanvasMutations>) => flushCanvasMutations(...args),
  resetCanvasPersistence: (...args: Parameters<typeof resetCanvasPersistence>) => resetCanvasPersistence(...args),
  retireCanvasPersistence: (...args: Parameters<typeof retireCanvasPersistence>) => retireCanvasPersistence(...args),
  waitForCanvasMutationsIdle: (...args: Parameters<typeof waitForCanvasMutationsIdle>) => waitForCanvasMutationsIdle(...args),
  seedCanvasBaselines: (...args: Parameters<typeof seedCanvasBaselines>) => seedCanvasBaselines(...args),
  persistNodeById: vi.fn(),
}));

vi.mock('@/modules/canvas/editor/persistence/localView', () => ({
  loadLocalView: (...args: Parameters<typeof loadLocalView>) => loadLocalView(...args),
}));

vi.mock('@/modules/canvas/panels/HistoryPanel', () => ({
  // 同 Minimap：用可见标记代替 null，好断言抽屉面板容器随 view 门控。
  HistoryPanel: () => <div data-testid="history-panel" />,
}));

vi.mock('@/modules/canvas/editor/agent/AgentPanel', () => ({
  // 页面级测试只关心「面板开没开、页面把宽度和 token 传下来没有」——sentinel 把
  // 这几个 props 暴露出来即可，真实面板的 WS/消息渲染与此无关。
  AgentPanel: ({ open, width, jwt }: { open: boolean; width: number; jwt: string }) =>
    open ? <div data-testid="agent-panel" data-width={width} data-jwt={jwt} /> : null,
  loadAgentPanelWidth: () => 480,
  saveAgentPanelWidth: () => {},
}));

// 落点固定在原点，本文件不测排布。claimAgentSlot 必须一起 mock：
// 漏一个导出，真跑到落点分支时是 "not a function" 而不是位置断言失败。
vi.mock('@/modules/canvas/editor/agent/dropPoint', () => ({
  agentDropPointFor: () => ({ x: 0, y: 0 }),
  claimAgentSlot: () => 0,
}));

vi.mock('@/modules/canvas/panels/LibraryPanel', () => ({
  LibraryPanel: () => null,
}));

vi.mock('@/modules/canvas/storyboard/StoryboardView', () => ({
  // 真实 StoryboardView 会拉 store.nodes 分组渲染——这里只关心它挂载/卸载
  // 是否跟着 view 走，用 sentinel 代替，避免把虚拟列表等无关实现细节
  // 拖进这个页面级测试。
  StoryboardView: () => <div data-testid="storyboard-view" />,
}));

vi.mock('@/shared/api', async () => {
  const actual = await vi.importActual<typeof import('@/shared/api')>('@/shared/api');
  return {
    ...actual,
    getProject: (...args: Parameters<typeof getProject>) => getProject(...args),
    updateProject: (...args: Parameters<typeof updateProject>) => updateProject(...args),
    getSnapshot: (...args: Parameters<typeof getSnapshot>) => getSnapshot(...args),
    // 模型目录与本文件无关；不 mock 的话会真发 XHR，jsdom 报一串连接错误噪声。
    listRuntimeModels: vi.fn(async () => []),
  };
});

function makeAxiosError(status: number): AxiosError {
  return {
    isAxiosError: true,
    name: 'AxiosError',
    message: `Request failed with status code ${status}`,
    toJSON: () => ({}),
    config: { headers: new AxiosHeaders() } as InternalAxiosRequestConfig,
    response: {
      status,
      statusText: status === 404 ? 'Not Found' : 'Forbidden',
      headers: {},
      config: { headers: new AxiosHeaders() } as InternalAxiosRequestConfig,
      data: {},
    },
  } as AxiosError;
}

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  });
}

function renderPage(queryClient = createTestQueryClient()) {

  const result = render(
    <QueryClientProvider client={queryClient}>
      <AntApp>
        <MemoryRouter initialEntries={['/canvas/14']}>
          <Routes>
            <Route path="/canvas/:projectId" element={<CanvasEditorPage />} />
            <Route path="/canvas" element={<div>canvas-projects-page</div>} />
          </Routes>
        </MemoryRouter>
      </AntApp>
    </QueryClientProvider>,
  );
  return { ...result, queryClient };
}

describe('CanvasEditorPage access guard', () => {
  beforeEach(() => {
    messageError.mockReset();
    getProject.mockResolvedValue({ id: 14, name: 'Secret Project' });
    getSnapshot.mockReset();
    loadMock.mockReset();
    configureCanvasPersistence.mockClear();
    setCanvasOwnerToken.mockClear();
    resetCanvasPersistence.mockClear();
    loadLocalView.mockClear();
  });

  it('不抢锁：直接用固定的本地 ownerToken 配好持久化，并用本地 view 替代服务端 view 加载画布', async () => {
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [], view: { x: 999, y: 999, zoom: 2 } },
      version: 1,
      syncedAt: '2026-07-15T00:00:00.000Z',
    });

    renderPage();

    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    expect(configureCanvasPersistence).toHaveBeenCalledWith(expect.objectContaining({ projectID: 14 }));
    expect(setCanvasOwnerToken).toHaveBeenCalledWith(LOCAL_OWNER_TOKEN);
    // 先 configure 再设 token——反过来会被 configure 里的兜底 reset 抹掉。
    expect(configureCanvasPersistence.mock.invocationCallOrder[0]).toBeLessThan(
      setCanvasOwnerToken.mock.invocationCallOrder[0],
    );
    expect(loadMock).toHaveBeenCalledWith({ nodes: [], connections: [], view: { x: 7, y: 8, zoom: 1.2 } });
  });

  // final-review I-1：sidecar 自动重启时 SidecarGate 卸掉整棵树再重挂。
  it('卸载时走 retire（先 flush debounce 中的 patch 再 reset），而不是直接 reset 丢弃', async () => {
    getSnapshot.mockResolvedValue({ payload: { nodes: [], connections: [] }, version: 1 });
    retireCanvasPersistence.mockClear();
    const { unmount } = renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());

    unmount();

    expect(retireCanvasPersistence).toHaveBeenCalledTimes(1);
    expect(resetCanvasPersistence).not.toHaveBeenCalled();
  });

  it('重挂后先等上一棵树留下的 mutation 落定，再拉快照', async () => {
    let drain!: () => void;
    waitForCanvasMutationsIdle.mockImplementationOnce(() => new Promise<undefined>((resolve) => {
      drain = () => resolve(undefined);
    }));
    getSnapshot.mockResolvedValue({ payload: { nodes: [], connections: [] }, version: 1 });

    renderPage();
    await waitFor(() => expect(waitForCanvasMutationsIdle).toHaveBeenCalled());
    await Promise.resolve();
    expect(getSnapshot).not.toHaveBeenCalled();

    drain();
    await waitFor(() => expect(getSnapshot).toHaveBeenCalledWith(14));
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
  });

  it('snapshot 返回 404 时不渲染编辑器并跳回 /canvas', async () => {
    getSnapshot.mockRejectedValue(makeAxiosError(404));

    renderPage();

    await waitFor(() => {
      expect(screen.getByText('canvas-projects-page')).toBeInTheDocument();
    });

    expect(screen.queryByTestId('canvas-editor')).not.toBeInTheDocument();
    expect(messageError).toHaveBeenCalledWith('该画布不存在或你无权访问');
  });

  it('保存成功更新 snapshot 缓存时不重新 load 当前画布', async () => {
    getSnapshot.mockResolvedValue({
      payload: { nodes: [{ id: 'n1', type: 'gen-image', x: 0, y: 0, width: 240, height: 200 }], connections: [], view: { x: 0, y: 0, zoom: 1 } },
      version: 1,
      syncedAt: '2026-07-15T00:00:00.000Z',
    });
    const { queryClient } = renderPage();

    await waitFor(() => expect(loadMock).toHaveBeenCalledTimes(1));

    queryClient.setQueryData(['snapshot', 14], {
      payload: { nodes: [{ id: 'n1', type: 'gen-image', x: 0, y: 0, width: 240, height: 200, settings: { prompt: 'new' } }], connections: [], view: { x: 0, y: 0, zoom: 1 } },
      version: 2,
      syncedAt: '2026-07-15T00:01:00.000Z',
    });

    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    expect(loadMock).toHaveBeenCalledTimes(1);
  });
});

describe('CanvasEditorPage 顶栏项目名就地改名', () => {
  beforeEach(() => {
    messageError.mockReset();
    getProject.mockResolvedValue({ id: 14, name: 'Secret Project', description: '原描述' });
    updateProject.mockReset();
    updateProject.mockResolvedValue({ id: 14, name: '新名字', description: '原描述' });
    getSnapshot.mockReset();
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } },
      version: 1,
      syncedAt: '2026-07-15T00:00:00.000Z',
    });
    loadMock.mockReset();
  });

  async function startEditing() {
    renderPage();
    await waitFor(() => expect(screen.getByText('Secret Project')).toBeInTheDocument());
    fireEvent.click(screen.getByText('Secret Project'));
    return screen.getByLabelText('项目名称') as HTMLInputElement;
  }

  it('点击项目名 → 输入新名 → 回车提交，请求体带回原 description（PUT 整行覆盖，不带会清空描述）', async () => {
    const input = await startEditing();
    expect(input.value).toBe('Secret Project');

    fireEvent.change(input, { target: { value: '  新名字  ' } });
    fireEvent.blur(input);

    // 提交的是 trim 后的名字 + 原 description
    await waitFor(() =>
      expect(updateProject).toHaveBeenCalledWith(14, { name: '新名字', description: '原描述' }),
    );
    // 输入框退场，回到展示态
    expect(screen.queryByLabelText('项目名称')).not.toBeInTheDocument();
  });

  it('清空名字提交：不发请求，还原旧名', async () => {
    const input = await startEditing();

    fireEvent.change(input, { target: { value: '   ' } });
    fireEvent.blur(input);

    expect(updateProject).not.toHaveBeenCalled();
    expect(screen.getByText('Secret Project')).toBeInTheDocument();
  });

  it('名字没改动就失焦：不发请求', async () => {
    const input = await startEditing();

    fireEvent.blur(input);

    expect(updateProject).not.toHaveBeenCalled();
    expect(screen.getByText('Secret Project')).toBeInTheDocument();
  });

  // 点画布不会触发 blur：CanvasEditor 在 mousedown 里 preventDefault（平移/框选
  // 起手式），把浏览器「点别处 → 原输入框失焦」的默认行为掐掉了。所以这条不许
  // 用 fireEvent.blur 模拟——直接对 document 派发 pointerdown，走的是编辑态挂在
  // capture 阶段的收口监听，这才是「点画布保存」的真实路径。
  it('点画布（只有 pointerdown、没有 blur）也要提交', async () => {
    const input = await startEditing();

    fireEvent.change(input, { target: { value: '点画布保存' } });
    fireEvent.pointerDown(document.body);

    await waitFor(() =>
      expect(updateProject).toHaveBeenCalledWith(14, { name: '点画布保存', description: '原描述' }),
    );
    expect(screen.queryByLabelText('项目名称')).not.toBeInTheDocument();
  });

  it('pointerdown 提交后浏览器补发 blur：同一次编辑只发一次请求', async () => {
    const input = await startEditing();

    fireEvent.change(input, { target: { value: '只提交一次' } });
    // 点顶栏其他按钮时两条收口路径都会走到：先 document pointerdown，
    // 浏览器随后把焦点挪走触发 blur。
    fireEvent.pointerDown(document.body);
    fireEvent.blur(input);

    await waitFor(() => expect(updateProject).toHaveBeenCalledTimes(1));
    expect(updateProject).toHaveBeenCalledWith(14, { name: '只提交一次', description: '原描述' });
  });

  it('Esc 放弃编辑：即使已输入新名也不提交', async () => {
    const input = await startEditing();

    fireEvent.change(input, { target: { value: '改了但反悔' } });
    fireEvent.keyDown(input, { key: 'Escape' });
    fireEvent.blur(input);

    expect(updateProject).not.toHaveBeenCalled();
    expect(screen.getByText('Secret Project')).toBeInTheDocument();
  });
});

describe('CanvasEditorPage 视图切换（工作流 / 故事板）', () => {
  beforeEach(() => {
    messageError.mockReset();
    getProject.mockResolvedValue({ id: 14, name: 'Secret Project' });
    getSnapshot.mockReset();
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } },
      version: 1,
      syncedAt: '2026-07-15T00:00:00.000Z',
    });
    loadMock.mockReset();
    configureCanvasPersistence.mockClear();
    setCanvasOwnerToken.mockClear();
    resetCanvasPersistence.mockClear();
    loadLocalView.mockClear();
  });

  // 视图偏好记在 localStorage（全局一个键，所有画布共用）。这条测的是真正的
  // 验收点：**下次进来直接渲染成上次选的那个**——只测 load/save 存取不够，
  // 那证明不了页面初始化真的用上了它（比如误写成 useEffect 事后切）。
  it('上次选了故事板：再进画布直接渲染故事板，不先闪一帧画布', async () => {
    window.localStorage.setItem('tvmao:canvas:viewMode', 'storyboard');
    renderPage();

    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    // 首次渲染就是故事板：画布的工具面板一次都没出现过
    expect(screen.getByTestId('storyboard-view')).toBeInTheDocument();
    expect(screen.queryByText('sidebar')).not.toBeInTheDocument();
    expect(screen.queryByTestId('minimap')).not.toBeInTheDocument();

    window.localStorage.clear();
  });

  it('切换视图会写回 localStorage', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: '故事板' }));
    expect(window.localStorage.getItem('tvmao:canvas:viewMode')).toBe('storyboard');

    fireEvent.click(screen.getByRole('button', { name: '工作流' }));
    expect(window.localStorage.getItem('tvmao:canvas:viewMode')).toBe('canvas');

    window.localStorage.clear();
  });

  it('切到故事板：覆盖层挂 data-storyboard-open，画布工具面板（Sidebar/抽屉容器/Minimap）隐藏；切回画布：全部恢复', async () => {
    const { container } = renderPage();

    // 先等画布就绪（owner 接管完成），默认是画布视图。
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    expect(screen.getByText('sidebar')).toBeInTheDocument();
    expect(screen.getByTestId('minimap')).toBeInTheDocument();
    expect(screen.getByTestId('history-panel')).toBeInTheDocument();
    expect(screen.getByTestId('shortcuts-strip')).toBeInTheDocument();
    expect(screen.queryByTestId('storyboard-view')).not.toBeInTheDocument();
    expect(container.querySelector('[data-storyboard-open]')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '故事板' }));

    // 故事板出现，画布的工具面板全部退场。
    expect(screen.getByTestId('storyboard-view')).toBeInTheDocument();
    expect(screen.queryByText('sidebar')).not.toBeInTheDocument();
    expect(screen.queryByTestId('minimap')).not.toBeInTheDocument();
    expect(screen.queryByTestId('history-panel')).not.toBeInTheDocument();
    // 回归③：ShortcutsHint 是 fixed + z-40，比故事板覆盖层的 z-30 还高，
    // 漏了门控就会浮在故事板内容之上，展示已被停用的画布快捷键。
    expect(screen.queryByTestId('shortcuts-strip')).not.toBeInTheDocument();
    // 覆盖层必须带这个标记——CanvasEditor 的全局键盘监听靠它停用，
    // 否则被盖住的画布节点会被 Delete/Cmd+Z 静默改掉（回归①）。
    expect(container.querySelector('[data-storyboard-open]')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '工作流' }));

    // 切回画布：画布本身全程没卸载（canvas-editor 一直在），工具面板恢复。
    expect(screen.getByTestId('canvas-editor')).toBeInTheDocument();
    expect(screen.getByText('sidebar')).toBeInTheDocument();
    expect(screen.getByTestId('minimap')).toBeInTheDocument();
    expect(screen.getByTestId('history-panel')).toBeInTheDocument();
    expect(screen.getByTestId('shortcuts-strip')).toBeInTheDocument();
    expect(screen.queryByTestId('storyboard-view')).not.toBeInTheDocument();
    expect(container.querySelector('[data-storyboard-open]')).not.toBeInTheDocument();
  });

  it('切到故事板自动打开 Agent 面板，覆盖层按面板宽度向左让位', async () => {
    const { container } = renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());

    // 画布视图下不会自动开。
    expect(screen.queryByTestId('agent-panel')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '故事板' }));

    // 面板自动打开，页面把宽度真源传给了它。
    const panel = screen.getByTestId('agent-panel');
    expect(panel.getAttribute('data-width')).toBe('480');
    // 覆盖层用 paddingRight 给面板让位——故事板内容被挤压，不是被盖住。
    const overlay = container.querySelector('[data-storyboard-open]') as HTMLElement;
    expect(overlay.style.paddingRight).toBe('480px');

    // 手动关掉后不会被强制重开（自动打开只在切入瞬间触发一次）。
    // 这里没渲染关闭按钮，直接走「切出去再切回来会再开」的对偶断言。
    fireEvent.click(screen.getByRole('button', { name: '工作流' }));
    fireEvent.click(screen.getByRole('button', { name: '故事板' }));
    expect(screen.getByTestId('agent-panel')).toBeInTheDocument();

    // 视图偏好是**全局**键，不清的话本文件后面所有用例都会在故事板模式下跑。
    window.localStorage.clear();
  });
});

describe('CanvasEditorPage 桌面端（spec F9、X1–X4）', () => {
  beforeEach(() => {
    messageError.mockReset();
    localStorage.clear();
    getProject.mockResolvedValue({ id: 14, name: 'Secret Project' });
    getSnapshot.mockReset();
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [] },
      version: 1,
      syncedAt: '2026-07-15T00:00:00.000Z',
    });
    loadMock.mockReset();
    flushCanvasMutations.mockClear();
    configureCanvasPersistence.mockClear();
    agentClientCtor.mockClear();
  });
  afterEach(() => {
    resetSidecarSessionForTest();
    localStorage.clear();
  });

  it('「所有项目」打开项目弹框：不跳转、不 flush、画布保持挂载', async () => {
    useProjectsModal.setState({ open: false });
    renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: '所有项目' }));

    expect(useProjectsModal.getState().open).toBe(true);
    expect(screen.getByTestId('canvas-editor')).toBeInTheDocument();
    expect(screen.queryByText('canvas-projects-page')).toBeNull();
    expect(flushCanvasMutations).not.toHaveBeenCalled();
  });

  it('「设置」打开设置弹框：不跳转、不 flush、画布保持挂载', async () => {
    useSettingsModal.setState({ open: false, tab: 'general' });
    flushCanvasMutations.mockClear();
    renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: '设置' }));

    expect(useSettingsModal.getState().open).toBe(true);
    expect(screen.getByTestId('canvas-editor')).toBeInTheDocument();
    expect(screen.queryByText('canvas-projects-page')).toBeNull();
    expect(flushCanvasMutations).not.toHaveBeenCalled();
  });

  it('画布载入成功后记为「上一次打开的项目」', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    await waitFor(() => expect(loadLastProjectId()).toBe(14));
  });

  it('快照 404（项目已删除）时不记为上一次打开的项目', async () => {
    getSnapshot.mockRejectedValue(makeAxiosError(404));
    renderPage();
    await waitFor(() => expect(screen.getByText('canvas-projects-page')).toBeInTheDocument());
    expect(loadLastProjectId()).toBeNull();
  });

  it('顶栏没有积分、没有发布/分享入口、没有回首页入口', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());

    expect(screen.queryByText(/积分/)).not.toBeInTheDocument();
    expect(screen.queryByTitle('发布与分享')).not.toBeInTheDocument();
    expect(screen.queryByTitle('回到首页')).not.toBeInTheDocument();
    expect(screen.queryByText('返回')).not.toBeInTheDocument();
  });

  it('页面本身不建 WebSocket；Agent 面板拿到的是本次启动的 sidecar token', async () => {
    setSidecarSession({ port: 50001, token: 'sidecar-tok' });
    renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    expect(agentClientCtor).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: /Agent/ }));
    expect(screen.getByTestId('agent-panel').getAttribute('data-jwt')).toBe('sidecar-tok');
  });

  it('局部 mutation 保存出错时提示「保存失败」', async () => {
    renderPage();
    await waitFor(() => expect(configureCanvasPersistence).toHaveBeenCalled());
    const { onError } = configureCanvasPersistence.mock.calls[0][0] as { onError: (err: unknown) => void };
    onError(new Error('boom'));
    expect(messageError).toHaveBeenCalledWith('保存失败');
  });
});
