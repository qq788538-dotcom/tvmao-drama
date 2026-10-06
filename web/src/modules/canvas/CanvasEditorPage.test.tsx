import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { AxiosHeaders, type AxiosError, type InternalAxiosRequestConfig } from 'axios';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CanvasEditorPage } from './CanvasEditorPage';
import { resetCanvasPresence, useCanvasPresenceStore } from './editor/presenceStore';
import { isTransientChannelOpen, sendDragTransient, setTransientChannel } from './editor/transientChannel';

const messageError = vi.fn();
const loadMock = vi.fn();

const getSnapshot = vi.fn();
const getProject = vi.fn();
const updateProject = vi.fn();
const fetchMe = vi.fn();
const getToken = vi.fn(() => 'token');
const claimCanvasOwner = vi.fn();
const heartbeatCanvasOwner = vi.fn(async () => undefined);
const probeCanvasOwner = vi.fn();
const getAuthContexts = vi.fn(async () => ({ currentTeamId: 0, teams: [] }));
const listTeamMembers = vi.fn(async () => []);
const setReadOnly = vi.fn();
const applyRemoteOp = vi.fn();
const applyTransientPositions = vi.fn();

// 观看模式的 WS：页面只在观看态建连，测试用它既能断言「编辑者一条都不建」，
// 又能把 canvas_changed 帧灌进页面。
const agentClientCtor = vi.fn();
const agentClientClose = vi.fn();
// 瞬态帧的出口：页面把它接到模块级通道上（T4），节点组件从那里发帧。
const sendCanvasTransient = vi.fn(() => true);
let lastFrameHandler: ((f: unknown) => void) | null = null;
let lastOpenHandler: (() => void) | null = null;
let lastCloseHandler: (() => void) | null = null;
vi.mock('@/modules/canvas/editor/agent/agentClient', () => ({
  AgentClient: class {
    constructor(opts: unknown) { agentClientCtor(opts); }
    onFrame(h: (f: unknown) => void) { lastFrameHandler = h; }
    onOpen(h: () => void) { lastOpenHandler = h; }
    onClose(h: () => void) { lastCloseHandler = h; }
    connect() {}
    close() { agentClientClose(); }
    sendCanvasTransient(...args: unknown[]) { return sendCanvasTransient(...(args as [])); }
  },
}));

const configureCanvasPersistence = vi.fn();
const setCanvasOwnerToken = vi.fn();
const getCanvasOwnerToken = vi.fn(() => 'owner-token');
// 形参带上 cb：观看模式那组测试要把这个回调抓出来手动触发「失去编辑权」，
// 声明成无参的话 mockImplementation 连类型都过不去。
const onCanvasOwnerLost = vi.fn((_cb: () => void) => () => undefined);
const flushCanvasMutations = vi.fn(async () => undefined);
const resetCanvasPersistence = vi.fn();
const seedCanvasBaselines = vi.fn();
const loadLocalView = vi.fn(() => ({ x: 7, y: 8, zoom: 1.2 }));
// 真实 antd 的 App.useApp() 每次调用返回同一个 message API 对象（走 context，
// 引用稳定）。这里必须复刻这个稳定性：CanvasEditorPage 的 claim-owner effect
// 把 message 放进依赖数组，如果 mock 每次渲染都 new 一个新对象，任何跟
// claim-owner 无关的 state 更新（比如本任务新加的 view 切换）都会误触发
// effect 重跑一次 claimCanvasOwner，页面短暂闪回 Loading——这不是待测代码
// 的 bug，是 mock 没有还原真实契约。
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
  // nodes:[] 补进 stub——真实 store 里 nodes 初始值恒为 []（永远不是
  // undefined），TvShowSubmitModal 在这个页面长挂载并订阅 s.nodes（历史图库
  // 候选集），stub 缺这个字段会让它在这批测试里读到 undefined，类型契约在
  // 测试里也得成立，不能靠业务代码兜底 `?? []` 掩盖。
  // view 与 getState：观看模式刷新快照时页面读 getState().view 保持观看者
  // 当前视角，这个值必须跟首屏加载用的 loadLocalView 不同才断言得出来。
  const state = () => ({ load: loadMock, nodes: [], setReadOnly, applyRemoteOp, applyTransientPositions, view: { x: 0, y: 0, zoom: 1 } });
  return {
    useCanvasStore: Object.assign(
      (selector: (s: ReturnType<typeof state>) => unknown) => selector(state()),
      { getState: () => state() },
    ),
  };
});

vi.mock('@/modules/canvas/editor/persistence/canvasMutations', () => ({
  configureCanvasPersistence: (...args: Parameters<typeof configureCanvasPersistence>) => configureCanvasPersistence(...args),
  setCanvasOwnerToken: (...args: Parameters<typeof setCanvasOwnerToken>) => setCanvasOwnerToken(...args),
  getCanvasOwnerToken: () => getCanvasOwnerToken(),
  onCanvasOwnerLost: (...args: Parameters<typeof onCanvasOwnerLost>) => onCanvasOwnerLost(...args),
  flushCanvasMutations: (...args: Parameters<typeof flushCanvasMutations>) => flushCanvasMutations(...args),
  resetCanvasPersistence: (...args: Parameters<typeof resetCanvasPersistence>) => resetCanvasPersistence(...args),
  seedCanvasBaselines: (...args: Parameters<typeof seedCanvasBaselines>) => seedCanvasBaselines(...args),
}));

vi.mock('@/modules/canvas/editor/persistence/localView', () => ({
  loadLocalView: (...args: Parameters<typeof loadLocalView>) => loadLocalView(...args),
}));

vi.mock('@/modules/canvas/panels/HistoryPanel', () => ({
  // 同 Minimap：用可见标记代替 null，好断言抽屉面板容器随 view 门控。
  HistoryPanel: () => <div data-testid="history-panel" />,
}));

vi.mock('@/modules/canvas/editor/agent/AgentPanel', () => ({
  // 页面级测试只关心「面板开没开、页面把宽度传下来没有」——sentinel 把这
  // 两个 props 暴露出来即可，真实面板的 WS/消息渲染与此无关。
  AgentPanel: ({ open, width }: { open: boolean; width: number }) =>
    open ? <div data-testid="agent-panel" data-width={width} /> : null,
  loadAgentPanelWidth: () => 480,
  saveAgentPanelWidth: () => {},
}));

// 落点固定在原点，本文件测的是瞬态层不是排布。claimAgentSlot 必须一起 mock：
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

vi.mock('@/modules/canvas/panels/PublishWorkflowModal', () => ({
  PublishWorkflowModal: () => null,
}));

vi.mock('@/shared/api', async () => {
  const actual = await vi.importActual<typeof import('@/shared/api')>('@/shared/api');
  return {
    ...actual,
    getProject: (...args: Parameters<typeof getProject>) => getProject(...args),
    updateProject: (...args: Parameters<typeof updateProject>) => updateProject(...args),
    getSnapshot: (...args: Parameters<typeof getSnapshot>) => getSnapshot(...args),
    getToken: () => getToken(),
    me: (...args: Parameters<typeof fetchMe>) => fetchMe(...args),
    claimCanvasOwner: (...args: Parameters<typeof claimCanvasOwner>) => claimCanvasOwner(...args),
    heartbeatCanvasOwner: (...args: Parameters<typeof heartbeatCanvasOwner>) => heartbeatCanvasOwner(...args),
    probeCanvasOwner: (...args: Parameters<typeof probeCanvasOwner>) => probeCanvasOwner(...args),
    getAuthContexts: (...args: Parameters<typeof getAuthContexts>) => getAuthContexts(...args),
    listTeamMembers: (...args: Parameters<typeof listTeamMembers>) => listTeamMembers(...args),
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
    fetchMe.mockResolvedValue({ balanceCredits: 10 });
    getSnapshot.mockReset();
    loadMock.mockReset();
    claimCanvasOwner.mockReset();
    claimCanvasOwner.mockResolvedValue({ ownerToken: 'owner-token', expiresAt: '2026-07-15T00:00:30.000Z' });
    heartbeatCanvasOwner.mockClear();
    configureCanvasPersistence.mockClear();
    setCanvasOwnerToken.mockClear();
    onCanvasOwnerLost.mockClear();
    resetCanvasPersistence.mockClear();
    loadLocalView.mockClear();
  });

  it('接管 owner 并用本地 view 替代服务端 view 加载画布', async () => {
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [], view: { x: 999, y: 999, zoom: 2 } },
      version: 1,
      syncedAt: '2026-07-15T00:00:00.000Z',
    });

    renderPage();

    await waitFor(() => expect(claimCanvasOwner).toHaveBeenCalledWith(14));
    expect(setCanvasOwnerToken).toHaveBeenCalledWith('owner-token');
    expect(loadMock).toHaveBeenCalledWith({ nodes: [], connections: [], view: { x: 7, y: 8, zoom: 1.2 } });
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
    fetchMe.mockResolvedValue({ balanceCredits: 10 });
    getSnapshot.mockReset();
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } },
      version: 1,
      syncedAt: '2026-07-15T00:00:00.000Z',
    });
    loadMock.mockReset();
    claimCanvasOwner.mockReset();
    claimCanvasOwner.mockResolvedValue({ ownerToken: 'owner-token', expiresAt: '2026-07-15T00:00:30.000Z' });
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
    fetchMe.mockResolvedValue({ balanceCredits: 10 });
    getSnapshot.mockReset();
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } },
      version: 1,
      syncedAt: '2026-07-15T00:00:00.000Z',
    });
    loadMock.mockReset();
    claimCanvasOwner.mockReset();
    claimCanvasOwner.mockResolvedValue({ ownerToken: 'owner-token', expiresAt: '2026-07-15T00:00:30.000Z' });
    heartbeatCanvasOwner.mockClear();
    configureCanvasPersistence.mockClear();
    setCanvasOwnerToken.mockClear();
    onCanvasOwnerLost.mockClear();
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

    // 视图偏好是**全局**键，不清的话本文件后面所有用例都会在故事板模式下跑
    // （曾经真发生过：presence 头像条那条用例其实一直是在故事板下断言的）。
    window.localStorage.clear();
  });
});

// 观看模式与个人锁路径。
//
// 二期后**观看模式只剩「身份未知」一个入口**：contexts 拉失败时不敢按团队直写
// （万一其实是个人身份就少了那把锁），照一期走 probe → 队友持锁则只读旁观。
// 团队身份的观看/接管/失锁降级已整体下线（团队直写，见下一个 describe）。
// 个人身份的每一条分支都必须与今天逐字一致（零回归），下面成对断言。
describe('CanvasEditorPage 观看模式与个人锁路径', () => {
  const reload = vi.fn();
  let ownerLostCb: (() => void) | null = null;

  const teamMate = { held: true, editorActorId: 42, isSelf: false, staleSeconds: 3 };

  // 身份未知 = /auth/contexts 拉失败。它保留着一期那条 probe/观看/接管全路径。
  function beUnknownIdentity() {
    getAuthContexts.mockRejectedValue(new Error('500'));
  }

  function beTeamIdentity() {
    getAuthContexts.mockResolvedValue({
      currentTeamId: 5,
      teams: [{ id: 5, uuid: 'u', name: '工作室', role: 'member' }],
    } as never);
  }

  beforeEach(() => {
    messageError.mockReset();
    getProject.mockResolvedValue({ id: 14, name: 'Secret Project' });
    fetchMe.mockResolvedValue({ balanceCredits: 10 });
    getSnapshot.mockReset();
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } },
      version: 1,
      syncedAt: '2026-08-19T00:00:00.000Z',
    });
    loadMock.mockReset();
    claimCanvasOwner.mockReset();
    claimCanvasOwner.mockResolvedValue({ ownerToken: '7:42:abcdef0123456789', expiresAt: '2026-08-19T00:00:30.000Z' });
    probeCanvasOwner.mockReset();
    probeCanvasOwner.mockResolvedValue({ held: false, editorActorId: 0, isSelf: false, staleSeconds: 0 });
    getAuthContexts.mockReset();
    getAuthContexts.mockResolvedValue({ currentTeamId: 0, teams: [] } as never);
    listTeamMembers.mockReset();
    listTeamMembers.mockResolvedValue([] as never);
    setReadOnly.mockReset();
    heartbeatCanvasOwner.mockClear();
    setCanvasOwnerToken.mockClear();
    configureCanvasPersistence.mockClear();
    getCanvasOwnerToken.mockReturnValue('owner-token');
    agentClientCtor.mockReset();
    agentClientClose.mockReset();
    lastFrameHandler = null;
    lastOpenHandler = null;
    reload.mockReset();
    ownerLostCb = null;
    onCanvasOwnerLost.mockImplementation((cb: () => void) => {
      ownerLostCb = cb;
      return () => undefined;
    });
    // jsdom 的 location.reload 会打 "Not implemented"，且没法直接 spy——整个
    // location 换成带 mock reload 的替身。
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...window.location, reload },
    });
  });

  // 横幅一律用通称：观看模式只可能出现在身份未知（currentTeamId=0）时，
  // 邮箱解析那条查询的前置条件（团队身份）永远不成立，二期已删。
  it('身份未知 + 队友正持锁：不 Claim，转只读，横幅显示通称', async () => {
    beUnknownIdentity();
    probeCanvasOwner.mockResolvedValue(teamMate);

    renderPage();

    await waitFor(() => expect(screen.getByText(/另一位成员 正在编辑，你在观看/)).toBeInTheDocument());
    expect(probeCanvasOwner).toHaveBeenCalledWith(14);
    // 核心：观看不抢锁——Claim 会把正在编辑的队友直接踢下线。
    expect(claimCanvasOwner).not.toHaveBeenCalled();
    expect(setReadOnly).toHaveBeenCalledWith(true);
    expect(setCanvasOwnerToken).not.toHaveBeenCalled();
    expect(configureCanvasPersistence).not.toHaveBeenCalled();
    // 邮箱解析已删，成员名单端点一次都不该被打。
    expect(listTeamMembers).not.toHaveBeenCalled();
  });

  it('个人身份：一次 probe 都不发，直接 Claim（零回归）', async () => {
    renderPage();

    await waitFor(() => expect(claimCanvasOwner).toHaveBeenCalledWith(14));
    expect(probeCanvasOwner).not.toHaveBeenCalled();
    expect(setReadOnly).not.toHaveBeenCalledWith(true);
    expect(screen.queryByText(/正在编辑，你在观看/)).not.toBeInTheDocument();
  });

  it('身份未知 + 持锁的是自己另一个 tab：照旧 steal-Claim，不进观看', async () => {
    beUnknownIdentity();
    probeCanvasOwner.mockResolvedValue({ ...teamMate, isSelf: true });

    renderPage();

    await waitFor(() => expect(claimCanvasOwner).toHaveBeenCalledWith(14));
    expect(screen.queryByText(/正在编辑，你在观看/)).not.toBeInTheDocument();
  });

  it('身份未知 + 持锁者早已没心跳（stale 超阈值）：照旧 Claim，不被一把没释放的锁顶住', async () => {
    beUnknownIdentity();
    probeCanvasOwner.mockResolvedValue({ ...teamMate, staleSeconds: 3600 });

    renderPage();

    await waitFor(() => expect(claimCanvasOwner).toHaveBeenCalledWith(14));
    expect(screen.queryByText(/正在编辑，你在观看/)).not.toBeInTheDocument();
  });

  // 【M1】claim effect 的身份守卫在 setClaimingOwner(true) 之前 return，所以
  // 身份未决时 claimingOwner 仍是 false——loading 闸必须自己含 identityResolved，
  // 否则冷加载（快照先于 contexts 回来）会露出一张没有 ownerToken 的可编辑画布：
  // 用户改了半天，mutation 全被静默丢弃。
  it('身份未决时页面停在 Loading，不露出可交互画布', async () => {
    getAuthContexts.mockReturnValue(new Promise(() => {}) as never); // 永不 resolve

    renderPage();

    await waitFor(() => expect(getSnapshot).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByText('Loading...')).toBeInTheDocument());
    expect(screen.queryByTestId('canvas-editor')).not.toBeInTheDocument();
    expect(claimCanvasOwner).not.toHaveBeenCalled();
  });

  // 【M2】contexts 拉失败时身份未知。当个人身份直接 Claim 等于拿「误踢正在
  // 编辑的同事」换「省一个 GET」——方向反了。probe 是只读的，isSelf 由服务端
  // 按 JWT actor 算，个人用户另一 tab 持锁仍 isSelf=true → 照旧 Claim。
  it('身份拉取失败：照样 probe，队友持锁时进观看而不是把人踢下线', async () => {
    getAuthContexts.mockRejectedValue(new Error('500'));
    probeCanvasOwner.mockResolvedValue(teamMate);

    renderPage();

    await waitFor(() => expect(probeCanvasOwner).toHaveBeenCalledWith(14));
    await waitFor(() => expect(screen.getByText(/正在编辑，你在观看/)).toBeInTheDocument());
    expect(claimCanvasOwner).not.toHaveBeenCalled();
  });

  it('身份拉取失败 + 锁没人持有：照旧 Claim（零回归）', async () => {
    getAuthContexts.mockRejectedValue(new Error('500'));

    renderPage();

    await waitFor(() => expect(claimCanvasOwner).toHaveBeenCalledWith(14));
    expect(probeCanvasOwner).toHaveBeenCalledWith(14);
  });

  it('probe 失败（探测端点挂了）：退回 Claim，不把人锁在门外', async () => {
    beUnknownIdentity();
    probeCanvasOwner.mockRejectedValue(new Error('503'));

    renderPage();

    await waitFor(() => expect(claimCanvasOwner).toHaveBeenCalledWith(14));
  });

  it('点「接管编辑」：调现有 Claim 并整页重载（走既有加载路径）', async () => {
    beUnknownIdentity();
    probeCanvasOwner.mockResolvedValue(teamMate);

    renderPage();
    await waitFor(() => expect(screen.getByRole('button', { name: '接管编辑' })).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: '接管编辑' }));

    await waitFor(() => expect(claimCanvasOwner).toHaveBeenCalledWith(14));
    await waitFor(() => expect(reload).toHaveBeenCalled());
  });

  // 二期：团队项目根本不持锁（不 Claim、不心跳、后端不校验），失锁事件对团队
  // 身份不该有任何后果——既不弹个人那个死弹窗，也不再降级成观看态（观看/接管
  // 整套 UI 对团队下线）。真弹了就是把一个能正常写的人关进只读。
  it('团队身份收到失锁事件：既不弹死弹窗也不转观看（团队没有锁这个概念）', async () => {
    beTeamIdentity();
    renderPage();
    await waitFor(() => expect(configureCanvasPersistence).toHaveBeenCalled());

    act(() => ownerLostCb?.());

    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    expect(screen.queryByText('画布已在其他页面继续编辑')).not.toBeInTheDocument();
    expect(screen.queryByText(/正在编辑，你在观看/)).not.toBeInTheDocument();
    expect(setReadOnly).not.toHaveBeenCalledWith(true);
    expect(probeCanvasOwner).not.toHaveBeenCalled();
  });

  it('个人身份失锁：仍是原来那个刷新弹窗（零回归）', async () => {
    renderPage();
    await waitFor(() => expect(claimCanvasOwner).toHaveBeenCalledWith(14));

    act(() => ownerLostCb?.());

    await waitFor(() => expect(screen.getByText('画布已在其他页面继续编辑')).toBeInTheDocument());
    expect(screen.queryByText(/正在编辑，你在观看/)).not.toBeInTheDocument();
  });

  it('编辑者不建观看用的 WS 连接，也就无从被 canvas_changed 打扰', async () => {
    renderPage();
    await waitFor(() => expect(claimCanvasOwner).toHaveBeenCalledWith(14));

    expect(agentClientCtor).not.toHaveBeenCalled();
  });

  it('观看端的 WS 握手必须带 project_id，否则 Hub 分发不到这条连接', async () => {
    beUnknownIdentity();
    probeCanvasOwner.mockResolvedValue(teamMate);

    renderPage();

    await waitFor(() =>
      expect(agentClientCtor).toHaveBeenCalledWith({ url: '/api/v1/agent/ws', token: 'token', projectId: 14 }),
    );
  });

  // 【L1】心跳 effect 判了 isObserving，但依赖数组漏了它：进观看是「probe 回来
  // 之后」发生的第二次 state 变化，effect 不重跑 → 那条 interval 会一直续着一把
  // 本页并不持有的锁（只剩 apiGuards 兜着）。
  describe('心跳', () => {
    beforeEach(() => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      getCanvasOwnerToken.mockReturnValue('owner-token'); // 上一个项目残留的 token
    });
    afterEach(() => {
      vi.useRealTimers();
    });

    it('进观看模式后心跳 interval 被清掉，不再替本页续锁', async () => {
      beUnknownIdentity();
      probeCanvasOwner.mockResolvedValue(teamMate);

      renderPage();
      await waitFor(() => expect(screen.getByText(/正在编辑，你在观看/)).toBeInTheDocument());

      await act(async () => { vi.advanceTimersByTime(30_000); });
      expect(heartbeatCanvasOwner).not.toHaveBeenCalled();
    });
  });

  describe('收帧刷新', () => {
    beforeEach(() => {
      // shouldAdvanceTime：让 promise/waitFor 照常推进，同时能手动快进防抖窗口。
      vi.useFakeTimers({ shouldAdvanceTime: true });
      // 观看者没有 ownerToken——source_mark 对比恒不成立，帧一律要刷新。
      getCanvasOwnerToken.mockReturnValue(null as never);
    });
    afterEach(() => {
      vi.useRealTimers();
    });

    async function renderObserving() {
      beUnknownIdentity();
      probeCanvasOwner.mockResolvedValue(teamMate);
      renderPage();
      await waitFor(() => expect(lastFrameHandler).not.toBeNull());
      await waitFor(() => expect(loadMock).toHaveBeenCalledTimes(1));
      return lastFrameHandler!;
    }

    it('版本更高的帧：防抖 500ms 后重拉一次快照，并按观看者当前视角重载', async () => {
      const onFrame = await renderObserving();
      getSnapshot.mockResolvedValue({
        payload: { nodes: [{ id: 'n1', type: 'gen-image', x: 0, y: 0, width: 240, height: 200 }], connections: [] },
        version: 2,
        syncedAt: '2026-08-19T00:05:00.000Z',
      });

      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 2, source_mark: 'deadbeef' }));
      // 防抖窗口没走完之前一次都不许拉——没有这条断言，把延迟改成 0 也照样绿。
      await act(async () => { vi.advanceTimersByTime(400); });
      expect(getSnapshot).toHaveBeenCalledTimes(1);
      await act(async () => { vi.advanceTimersByTime(100); });

      await waitFor(() => expect(getSnapshot).toHaveBeenCalledTimes(2));
      await waitFor(() => expect(loadMock).toHaveBeenCalledTimes(2));
      // 视角保持观看者当前的（store.getState().view），不是首屏那份 localStorage 视角，
      // 否则队友每改一次就把观看者弹回原点。
      // 第二个参数 preserveUiState：整拉是「对齐到服务端最新」，不是换画布——
      // 清掉选中会收起用户正开着的参数面板，清掉 undo 栈会让他刚做的几步撤不回去。
      expect(loadMock).toHaveBeenLastCalledWith(
        {
          nodes: [{ id: 'n1', type: 'gen-image', x: 0, y: 0, width: 240, height: 200 }],
          connections: [],
          view: { x: 0, y: 0, zoom: 1 },
        },
        { preserveUiState: true },
      );
    });

    it('连发多帧只合并成一次重拉（编辑者一次拖拽会推一串帧）', async () => {
      const onFrame = await renderObserving();
      getSnapshot.mockResolvedValue({ payload: { nodes: [], connections: [] }, version: 5, syncedAt: null });

      act(() => {
        onFrame({ type: 'canvas_changed', project_id: 14, version: 2 });
        onFrame({ type: 'canvas_changed', project_id: 14, version: 3 });
        onFrame({ type: 'canvas_changed', project_id: 14, version: 4 });
      });
      await act(async () => { vi.advanceTimersByTime(500); });

      await waitFor(() => expect(getSnapshot).toHaveBeenCalledTimes(2));
      await act(async () => { vi.advanceTimersByTime(2000); });
      expect(getSnapshot).toHaveBeenCalledTimes(2);
    });

    it('版本不高于本地已载的帧一律丢弃（回声 / 迟到帧）', async () => {
      const onFrame = await renderObserving();

      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 1 }));
      await act(async () => { vi.advanceTimersByTime(1000); });

      expect(getSnapshot).toHaveBeenCalledTimes(1);
    });

    it('别的项目的帧不刷新本项目（帧按用户维度分发，会串项目）', async () => {
      const onFrame = await renderObserving();

      act(() => onFrame({ type: 'canvas_changed', project_id: 99, version: 9 }));
      await act(async () => { vi.advanceTimersByTime(1000); });

      expect(getSnapshot).toHaveBeenCalledTimes(1);
    });

    // AgentClient 只重连、不补发断线期间的帧。不在重连时补拉一次的话，队友在
    // 断线窗口里的改动要一直等到他下一次改动推来新帧才可见——他就此收工的话，
    // 观看者屏幕上就永久停在旧版本。
    it('WS 重连成功后补拉一次快照；首次连上不重复拉', async () => {
      await renderObserving();
      expect(lastOpenHandler).not.toBeNull();

      act(() => lastOpenHandler!()); // 首次 open：快照刚加载完，不该再拉
      await act(async () => { vi.advanceTimersByTime(1000); });
      expect(getSnapshot).toHaveBeenCalledTimes(1);

      act(() => lastOpenHandler!()); // 重连
      await waitFor(() => expect(getSnapshot).toHaveBeenCalledTimes(2));
    });

    it('agent 会话帧不触发刷新（同一条 socket 多路复用）', async () => {
      const onFrame = await renderObserving();

      act(() => onFrame({ type: 'action_end', project_id: 14, thread_id: 't1' }));
      await act(async () => { vi.advanceTimersByTime(1000); });

      expect(getSnapshot).toHaveBeenCalledTimes(1);
    });
  });
});

// 二期：团队项目多人同时编辑。后端对团队身份跳过 owner.Require（并发写由
// MySQL 行锁串行化），页面据此下线整条单人锁链路，改成「直写 + 收 op 帧本地
// 合并 + version 缺口整拉兜底」。个人身份的零回归断言在上一个 describe 里。
describe('CanvasEditorPage 团队并发编辑', () => {
  const teamToken = () => {
    const calls = setCanvasOwnerToken.mock.calls;
    return calls[calls.length - 1][0] as string;
  };
  const markOf = (token: string) => (token.split(':').pop() ?? '').slice(0, 8);

  function beTeamIdentity() {
    getAuthContexts.mockResolvedValue({
      currentTeamId: 5,
      teams: [{ id: 5, uuid: 'u', name: '工作室', role: 'member' }],
    } as never);
  }

  beforeEach(() => {
    messageError.mockReset();
    getProject.mockResolvedValue({ id: 14, name: 'Secret Project' });
    fetchMe.mockResolvedValue({ balanceCredits: 10 });
    getSnapshot.mockReset();
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } },
      version: 1,
      syncedAt: '2026-08-19T00:00:00.000Z',
    });
    loadMock.mockReset();
    claimCanvasOwner.mockReset();
    probeCanvasOwner.mockReset();
    heartbeatCanvasOwner.mockClear();
    listTeamMembers.mockReset();
    setReadOnly.mockReset();
    applyRemoteOp.mockReset();
    seedCanvasBaselines.mockReset();
    configureCanvasPersistence.mockClear();
    setCanvasOwnerToken.mockReset();
    // 页面 setCanvasOwnerToken 之后，getCanvasOwnerToken 必须能读回同一个值——
    // 回声检测（source_mark == 自己指纹）整条链就架在这上面，mock 不还原这个
    // 因果关系的话，测出来的「跳过自己的帧」是假的。
    setCanvasOwnerToken.mockImplementation((token: string) => {
      getCanvasOwnerToken.mockReturnValue(token as never);
    });
    getCanvasOwnerToken.mockReturnValue(null as never);
    agentClientCtor.mockReset();
    agentClientClose.mockReset();
    lastFrameHandler = null;
    lastOpenHandler = null;
    getAuthContexts.mockReset();
    beTeamIdentity();
  });

  it('团队项目：不 probe、不 Claim，直接配好持久化就能写', async () => {
    renderPage();

    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    // 核心：团队项目不再抢锁——抢一次就把正在编辑的队友踢下线，而后端根本不看这把锁。
    expect(probeCanvasOwner).not.toHaveBeenCalled();
    expect(claimCanvasOwner).not.toHaveBeenCalled();
    expect(setReadOnly).not.toHaveBeenCalledWith(true);
    expect(configureCanvasPersistence).toHaveBeenCalledWith(
      expect.objectContaining({ projectID: 14 }),
    );
    // 观看/接管 UI 对团队下线。
    expect(screen.queryByText(/正在编辑，你在观看/)).not.toBeInTheDocument();
  });

  // 全体团队客户端若共用同一个常量 token，source_mark 会撞成同一个值 →
  // 每个人都把队友的 op 当成自己的回声跳过 → 协作静默失效且零报错。
  it('团队 ownerToken 是 team:<8位hex>，且每个标签页各不相同', async () => {
    const first = renderPage();
    await waitFor(() => expect(setCanvasOwnerToken).toHaveBeenCalled());
    const tokenA = teamToken();
    expect(tokenA).toMatch(/^team:[0-9a-f]{8}$/);

    first.unmount();
    renderPage();
    await waitFor(() => expect(setCanvasOwnerToken).toHaveBeenCalledTimes(2));
    expect(teamToken()).not.toBe(tokenA);
  });

  // 时序：configureCanvasPersistence 在换项目时会兜底 resetCanvasPersistence()，
  // 而那会清空 ownerToken。先设 token 再 configure 就等于把刚拿到的编辑权凭据
  // 交给 reset 抹掉——之后所有结构性 op 被 applyCanvasOp 静默丢弃（零报错）。
  it('先 configure 再设 ownerToken——反过来会被兜底 reset 抹掉', async () => {
    renderPage();
    await waitFor(() => expect(setCanvasOwnerToken).toHaveBeenCalled());

    expect(configureCanvasPersistence.mock.invocationCallOrder[0]).toBeLessThan(
      setCanvasOwnerToken.mock.invocationCallOrder[0],
    );
  });

  it('团队项目关闭心跳：那把锁没人读，续它只会污染 canvas_owner', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      renderPage();
      await waitFor(() => expect(configureCanvasPersistence).toHaveBeenCalled());

      await act(async () => { vi.advanceTimersByTime(60_000); });
      expect(heartbeatCanvasOwner).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('团队项目的编辑者也常连 WS（不再只有观看者连），握手带 project_id', async () => {
    renderPage();

    await waitFor(() =>
      expect(agentClientCtor).toHaveBeenCalledWith({ url: '/api/v1/agent/ws', token: 'token', projectId: 14 }),
    );
  });

  describe('收 op 帧', () => {
    // 卸载路径要在测试里手动触发（作废在飞的整拉），所以留住 render 句柄。
    let currentPage: ReturnType<typeof renderPage> | null = null;

    beforeEach(() => {
      currentPage = null;
      vi.useFakeTimers({ shouldAdvanceTime: true });
    });
    afterEach(() => {
      vi.useRealTimers();
    });

    async function renderTeamEditor() {
      currentPage = renderPage();
      await waitFor(() => expect(lastFrameHandler).not.toBeNull());
      await waitFor(() => expect(loadMock).toHaveBeenCalledTimes(1));
      // 首屏快照即基线（字段级 diff 的起点）。
      expect(seedCanvasBaselines).toHaveBeenCalledWith([]);
      return lastFrameHandler!;
    }

    it('版本连续且带 op：本地合并，一次快照都不拉', async () => {
      const onFrame = await renderTeamEditor();

      act(() => onFrame({
        type: 'canvas_changed',
        project_id: 14,
        version: 2,
        source_mark: 'deadbeef',
        op: { kind: 'patchNode', node_id: 'n1', patch: { settings: { prompt: '队友写的' } } },
      }));
      await act(async () => { vi.advanceTimersByTime(1000); });

      expect(applyRemoteOp).toHaveBeenCalledWith({
        kind: 'patchNode',
        nodeId: 'n1',
        patch: { settings: { prompt: '队友写的' } },
      });
      expect(getSnapshot).toHaveBeenCalledTimes(1); // 只有首屏那一次
    });

    it('deleteNodes 帧翻译成 nodeIds（连线级联由 store 的 applyRemoteOp 负责）', async () => {
      const onFrame = await renderTeamEditor();

      act(() => onFrame({
        type: 'canvas_changed',
        project_id: 14,
        version: 2,
        source_mark: 'deadbeef',
        op: { kind: 'deleteNodes', node_ids: ['n1', 'n2'] },
      }));

      expect(applyRemoteOp).toHaveBeenCalledWith({ kind: 'deleteNodes', nodeIds: ['n1', 'n2'] });
    });

    // 自己的写本地早已应用，重放一遍没意义；但 version 必须推进——不推的话
    // 队友的下一帧就成了「缺口」，每写一笔都整拉一次，本地合并等于白做。
    it('自己指纹的帧：不重放，但推进 version，随后队友的帧仍走本地合并', async () => {
      const onFrame = await renderTeamEditor();
      const mine = markOf(teamToken());

      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 2, source_mark: mine,
        op: { kind: 'deleteNodes', node_ids: ['n9'] } }));
      expect(applyRemoteOp).not.toHaveBeenCalled();

      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 3, source_mark: 'deadbeef',
        op: { kind: 'upsertNode', node: { id: 'n3', type: 'gen-image', x: 1, y: 2, width: 3, height: 4 } } }));
      await act(async () => { vi.advanceTimersByTime(1000); });

      expect(applyRemoteOp).toHaveBeenCalledTimes(1);
      expect(applyRemoteOp).toHaveBeenCalledWith({
        kind: 'upsertNode',
        node: { id: 'n3', type: 'gen-image', x: 1, y: 2, width: 3, height: 4 },
      });
      expect(getSnapshot).toHaveBeenCalledTimes(1);
    });

    it('version 缺口：不合并，防抖整拉并用新快照重播基线', async () => {
      const onFrame = await renderTeamEditor();
      getSnapshot.mockResolvedValue({
        payload: { nodes: [{ id: 'n1', type: 'gen-image', x: 0, y: 0, width: 240, height: 200 }], connections: [] },
        version: 7,
        syncedAt: '2026-08-19T00:05:00.000Z',
      });

      // 本地在 v1，来的是 v5——中间三帧没到，本地状态已与服务端分叉。
      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 5, source_mark: 'deadbeef',
        op: { kind: 'deleteNodes', node_ids: ['n1'] } }));
      expect(applyRemoteOp).not.toHaveBeenCalled();
      await act(async () => { vi.advanceTimersByTime(400); });
      expect(getSnapshot).toHaveBeenCalledTimes(1); // 防抖窗口没到，先不拉
      await act(async () => { vi.advanceTimersByTime(100); });

      await waitFor(() => expect(getSnapshot).toHaveBeenCalledTimes(2));
      await waitFor(() => expect(loadMock).toHaveBeenCalledTimes(2));
      // 整拉后基线必须换成这份服务端快照，否则下一次 diff 拿旧基线算，
      // 会把队友刚写的值当本地变更回滚回去。
      expect(seedCanvasBaselines).toHaveBeenLastCalledWith([
        { id: 'n1', type: 'gen-image', x: 0, y: 0, width: 240, height: 200 },
      ]);
      // 视角保持本页当前的，不弹回 localStorage 那份。
      // 第二个参数 preserveUiState：整拉是「对齐到服务端最新」，不是换画布——
      // 清掉选中会收起用户正开着的参数面板，清掉 undo 栈会让他刚做的几步撤不回去。
      expect(loadMock).toHaveBeenLastCalledWith(
        {
          nodes: [{ id: 'n1', type: 'gen-image', x: 0, y: 0, width: 240, height: 200 }],
          connections: [],
          view: { x: 0, y: 0, zoom: 1 },
        },
        { preserveUiState: true },
      );
    });

    // 滚动部署期间新版后端会发老客户端不认识的 kind；摘要序列化失败时 op 干脆
    // 缺席。两种情况都必须退回整拉，绝不能把认不出来的东西塞进画布。
    it('不认识的 op kind / 没有 op：退回整拉兜底', async () => {
      const onFrame = await renderTeamEditor();

      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 2, source_mark: 'deadbeef',
        op: { kind: 'reorderLayers', node_ids: ['n1'] } }));
      await act(async () => { vi.advanceTimersByTime(500); });
      await waitFor(() => expect(getSnapshot).toHaveBeenCalledTimes(2));
      expect(applyRemoteOp).not.toHaveBeenCalled();

      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 8, source_mark: 'deadbeef' }));
      await act(async () => { vi.advanceTimersByTime(500); });
      await waitFor(() => expect(getSnapshot).toHaveBeenCalledTimes(3));
      expect(applyRemoteOp).not.toHaveBeenCalled();
    });

    it('别的项目的帧不动本项目（帧按用户维度分发，会串项目）', async () => {
      const onFrame = await renderTeamEditor();

      act(() => onFrame({ type: 'canvas_changed', project_id: 99, version: 9,
        op: { kind: 'deleteNodes', node_ids: ['n1'] } }));
      await act(async () => { vi.advanceTimersByTime(1000); });

      expect(applyRemoteOp).not.toHaveBeenCalled();
      expect(getSnapshot).toHaveBeenCalledTimes(1);
    });

    // 【M1】两次整拉重叠时先发后到：旧响应会把新快照盖回去、loadedVersion 倒退、
    // seedCanvasBaselines 被钉在旧状态——下一次 persist 就把队友刚写的字段当成
    // 本地变更写回服务端（静默数据回滚）。守卫是「代次 + 版本」两道。
    it('两次整拉重叠：旧响应后到必须被丢弃，不许覆盖新快照/让版本倒退', async () => {
      const onFrame = await renderTeamEditor();
      let resolveOld: ((snap: unknown) => void) | null = null;
      let resolveNew: ((snap: unknown) => void) | null = null;
      getSnapshot
        .mockImplementationOnce(() => new Promise((r) => { resolveOld = r as typeof resolveOld; }))
        .mockImplementationOnce(() => new Promise((r) => { resolveNew = r as typeof resolveNew; }));

      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 5, source_mark: 'deadbeef' }));
      await act(async () => { vi.advanceTimersByTime(500); });
      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 9, source_mark: 'deadbeef' }));
      await act(async () => { vi.advanceTimersByTime(500); });
      expect(getSnapshot).toHaveBeenCalledTimes(3); // 首屏 + 两次整拉

      // 新的先回来，旧的后回来。
      await act(async () => {
        resolveNew!({ payload: { nodes: [{ id: 'new' }], connections: [] }, version: 9, syncedAt: null });
      });
      await act(async () => {
        resolveOld!({ payload: { nodes: [{ id: 'old' }], connections: [] }, version: 5, syncedAt: null });
      });

      expect(loadMock).toHaveBeenLastCalledWith(
        expect.objectContaining({ nodes: [{ id: 'new' }] }),
        { preserveUiState: true },
      );
      expect(seedCanvasBaselines).toHaveBeenLastCalledWith([{ id: 'new' }]);
      // 版本没被旧响应拉回 5：v10 正好是 9+1，还能继续本地合并。
      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 10, source_mark: 'deadbeef',
        op: { kind: 'deleteNodes', node_ids: ['new'] } }));
      expect(applyRemoteOp).toHaveBeenCalledWith({ kind: 'deleteNodes', nodeIds: ['new'] });
    });

    // 代次守卫拦不住这一条：整拉在飞的期间，乱序补齐的 op 帧把本地推得比快照还
    // 新（这些 op 不发新请求，代次没变）。响应落地就是拿旧内容盖新状态 +
    // 基线倒退，所以版本守卫是并列的第二道，不是重复。
    it('整拉在飞时本地已被 op 帧推得更新：回来的旧版本快照必须丢弃', async () => {
      const onFrame = await renderTeamEditor();
      let resolveStale: ((snap: unknown) => void) | null = null;
      getSnapshot.mockImplementationOnce(() => new Promise((r) => { resolveStale = r as typeof resolveStale; }));

      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 3, source_mark: 'deadbeef' }));
      await act(async () => { vi.advanceTimersByTime(500); });
      expect(getSnapshot).toHaveBeenCalledTimes(2); // 整拉已发出，响应还没回来

      // 期间乱序的 v2/v3 补到了，带 op，逐帧本地合并 → 本地已经在 v3。
      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 2, source_mark: 'deadbeef',
        op: { kind: 'deleteNodes', node_ids: ['a'] } }));
      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 3, source_mark: 'deadbeef',
        op: { kind: 'deleteNodes', node_ids: ['b'] } }));
      expect(applyRemoteOp).toHaveBeenCalledTimes(2);

      const loadsBefore = loadMock.mock.calls.length;
      const seedsBefore = seedCanvasBaselines.mock.calls.length;
      await act(async () => {
        resolveStale!({ payload: { nodes: [{ id: 'stale' }], connections: [] }, version: 3, syncedAt: null });
      });

      expect(loadMock).toHaveBeenCalledTimes(loadsBefore);
      expect(seedCanvasBaselines).toHaveBeenCalledTimes(seedsBefore);
    });

    // 代次守卫的独立职责：离开这张画布。切项目/卸载时在飞的响应回来，版本号
    // 分属两条时间线（新项目的 v1 未必小于旧项目的 v9），只能靠代次作废。
    it('整拉在飞时页面卸载：响应回来不许再往 store 里灌快照', async () => {
      const onFrame = await renderTeamEditor();
      let resolveLate: ((snap: unknown) => void) | null = null;
      getSnapshot.mockImplementationOnce(() => new Promise((r) => { resolveLate = r as typeof resolveLate; }));

      act(() => onFrame({ type: 'canvas_changed', project_id: 14, version: 9, source_mark: 'deadbeef' }));
      await act(async () => { vi.advanceTimersByTime(500); });
      expect(getSnapshot).toHaveBeenCalledTimes(2);

      const loadsBefore = loadMock.mock.calls.length;
      currentPage!.unmount();
      await act(async () => {
        resolveLate!({ payload: { nodes: [{ id: 'late' }], connections: [] }, version: 9, syncedAt: null });
      });

      expect(loadMock).toHaveBeenCalledTimes(loadsBefore);
      expect(seedCanvasBaselines).not.toHaveBeenCalledWith([{ id: 'late' }]);
    });

    // 【M2】缺口态下后面每一帧都还是缺口，每帧都重置 500ms 短窗 → 帧间隔稳定
    // 小于 500ms 时（多队友 + agent 批量回写）整拉永远不发生，本地静默停在分叉
    // 状态。所以防抖必须带最大等待上限。
    it('密集缺口帧不断重置短窗：仍必须在最大等待内整拉一次', async () => {
      const onFrame = await renderTeamEditor();
      getSnapshot.mockResolvedValue({ payload: { nodes: [], connections: [] }, version: 30, syncedAt: null });

      let version = 5;
      // 帧落在 t=0/400/…/1600（间隔 400ms < 500ms 短窗），最后推进到 t=2000。
      // 纯尾防抖下最后一帧把窗口顶到 t=2100，此刻一次都还没拉；带 2s 最大等待
      // 则必须在 t=2000 这一刻拉出去。**断言必须是同步的**：跟一句 waitFor 的话
      // 它自己会把时间推过 2100，两种实现都绿，测试等于没写。
      for (let i = 0; i < 5; i += 1) {
        act(() => onFrame({ type: 'canvas_changed', project_id: 14, version, source_mark: 'deadbeef' }));
        version += 1;
        await act(async () => { vi.advanceTimersByTime(400); });
        if (i < 4) expect(getSnapshot).toHaveBeenCalledTimes(1); // 短窗一直被重置，还没到上限
      }

      expect(getSnapshot).toHaveBeenCalledTimes(2);
    });

    it('WS 重连后整拉兜底（断线窗口里的帧不会补发）', async () => {
      await renderTeamEditor();
      expect(lastOpenHandler).not.toBeNull();

      act(() => lastOpenHandler!()); // 首次 open：快照刚加载完，不该再拉
      await act(async () => { vi.advanceTimersByTime(1000); });
      expect(getSnapshot).toHaveBeenCalledTimes(1);

      act(() => lastOpenHandler!()); // 重连
      await waitFor(() => expect(getSnapshot).toHaveBeenCalledTimes(2));
    });
  });
});

// 瞬态三件套（T4）：拖拽实时流的收端、「正在编辑」提示、presence 头像条。
// 三者皆**团队 only**——个人项目零帧、零渲染。
describe('CanvasEditorPage 瞬态层（拖拽流 / 正在编辑 / presence）', () => {
  function beTeamIdentity() {
    getAuthContexts.mockResolvedValue({
      currentTeamId: 5,
      personalEmail: 'me@x.com',
      teams: [{ id: 5, uuid: 'u', name: '工作室', role: 'member' }],
    } as never);
  }

  beforeEach(() => {
    messageError.mockReset();
    getProject.mockResolvedValue({ id: 14, name: 'Secret Project' });
    fetchMe.mockResolvedValue({ balanceCredits: 10, email: 'team-shadow@team.internal' });
    getSnapshot.mockReset();
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } },
      version: 1,
      syncedAt: '2026-08-19T00:00:00.000Z',
    });
    loadMock.mockReset();
    claimCanvasOwner.mockReset();
    claimCanvasOwner.mockResolvedValue({ ownerToken: '7:42:abcdef0123456789', expiresAt: '2026-08-19T00:00:30.000Z' });
    probeCanvasOwner.mockReset();
    probeCanvasOwner.mockResolvedValue({ held: false, editorActorId: 0, isSelf: false, staleSeconds: 0 });
    applyTransientPositions.mockReset();
    applyRemoteOp.mockReset();
    setCanvasOwnerToken.mockReset();
    getCanvasOwnerToken.mockReturnValue(null as never);
    agentClientCtor.mockReset();
    sendCanvasTransient.mockClear();
    lastFrameHandler = null;
    lastOpenHandler = null;
    lastCloseHandler = null;
    getAuthContexts.mockReset();
    beTeamIdentity();
    resetCanvasPresence();
  });

  afterEach(() => {
    setTransientChannel(null);
    act(() => resetCanvasPresence());
  });

  async function renderTeam() {
    const page = renderPage();
    await waitFor(() => expect(lastFrameHandler).not.toBeNull());
    await waitFor(() => expect(loadMock).toHaveBeenCalledTimes(1));
    return { page, onFrame: lastFrameHandler! };
  }

  it('收 drag 帧：直接落内存位置（不走 applyRemoteOp、不整拉）', async () => {
    const { onFrame } = await renderTeam();

    act(() => onFrame({
      type: 'canvas_transient',
      project_id: 14,
      actor_user_id: 7,
      kind: 'drag',
      positions: [{ id: 'n1', x: 10, y: 20 }, { id: 'bad' }],
    }));

    // 坏条目被剔除，不会把节点弹到 NaN 坐标。
    expect(applyTransientPositions).toHaveBeenCalledWith([{ id: 'n1', x: 10, y: 20 }]);
    expect(applyRemoteOp).not.toHaveBeenCalled();
    expect(getSnapshot).toHaveBeenCalledTimes(1);
  });

  it('收 node_editing 帧：记进 presence，节点角标据此显示', async () => {
    const { onFrame } = await renderTeam();

    act(() => onFrame({
      type: 'canvas_transient', project_id: 14, actor_user_id: 7, kind: 'node_editing',
      node_id: 'n1', editing: true,
    }));
    expect(useCanvasPresenceStore.getState().editors.n1).toEqual({ actorUserId: 7 });

    act(() => onFrame({
      type: 'canvas_transient', project_id: 14, actor_user_id: 7, kind: 'node_editing',
      node_id: 'n1', editing: false,
    }));
    expect(useCanvasPresenceStore.getState().editors.n1).toBeUndefined();
  });

  it('不认识的瞬态 kind 静默忽略（种类还会增长，不该崩也不该整拉）', async () => {
    const { onFrame } = await renderTeam();

    act(() => onFrame({ type: 'canvas_transient', project_id: 14, kind: 'cursor', x: 1 }));

    expect(applyTransientPositions).not.toHaveBeenCalled();
    expect(getSnapshot).toHaveBeenCalledTimes(1);
  });

  it('别的项目的瞬态帧不动本项目', async () => {
    const { onFrame } = await renderTeam();

    act(() => onFrame({ type: 'canvas_transient', project_id: 99, kind: 'drag',
      positions: [{ id: 'n1', x: 1, y: 2 }] }));
    act(() => onFrame({ type: 'canvas_presence', project_id: 99,
      members: [{ actor_user_id: 3, email: 'a@x.com' }] }));

    expect(applyTransientPositions).not.toHaveBeenCalled();
    expect(useCanvasPresenceStore.getState().members).toEqual([]);
  });

  // 故事板是覆盖全屏的第二视图（z-30，内容 pt-14），头像条 z-40 会压在它顶部
  // 内容上；同为协作提示的节点角标已经在故事板模式隐藏，两者取舍必须一致。
  it('切到故事板后头像条不再浮着（与节点角标取舍一致）', async () => {
    const { onFrame } = await renderTeam();
    act(() => onFrame({
      type: 'canvas_presence',
      project_id: 14,
      members: [{ actor_user_id: 3, email: 'mate@x.com' }, { actor_user_id: 9, email: 'me@x.com' }],
    }));
    await waitFor(() => expect(document.querySelector('[data-presence-bar]')).not.toBeNull());

    fireEvent.click(screen.getByRole('button', { name: '故事板' }));
    expect(document.querySelector('[data-presence-bar]')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: '工作流' }));
    expect(document.querySelector('[data-presence-bar]')).not.toBeNull();
    window.localStorage.clear();
  });

  it('收 presence 全量名册：渲染头像条，自己那个标「我」', async () => {
    const { onFrame } = await renderTeam();

    act(() => onFrame({
      type: 'canvas_presence',
      project_id: 14,
      members: [{ actor_user_id: 3, email: 'mate@x.com' }, { actor_user_id: 9, email: 'me@x.com' }],
    }));

    await waitFor(() => expect(document.querySelector('[data-presence-bar]')).not.toBeNull());
    expect(document.querySelectorAll('[data-presence-member]')).toHaveLength(2);
    // 团队身份下 /me 是影子用户（team-shadow@team.internal），认自己只能靠
    // contexts 的 personalEmail——拿错就会把自己标成别人。
    expect(document.querySelector('[data-presence-member="9"]')!.textContent).toBe('我');
    expect(document.querySelector('[data-presence-member="3"]')!.textContent).toBe('M');
  });

  it('团队项目注册瞬态通道，帧经这条 WS 发出；页面卸载即注销', async () => {
    const { page } = await renderTeam();
    expect(isTransientChannelOpen()).toBe(true);

    sendDragTransient([{ id: 'n1', x: 5, y: 6 }]);
    expect(sendCanvasTransient).toHaveBeenCalledWith({
      type: 'canvas_transient', project_id: 14, kind: 'drag', positions: [{ id: 'n1', x: 5, y: 6 }],
    });

    page.unmount();
    expect(isTransientChannelOpen()).toBe(false);
  });

  // 【L3】名册是服务端在连接进出时推的全量快照，**没有 TTL**：断线期间队友进出
  // 一次都收不到，屏幕上会留着一份越来越假的名册（人早走了头像还在）。重连后
  // 服务端注册时会立刻重推全量，清空不会让名册少一帧。
  it('WS 断开即清名册与「正在编辑」角标', async () => {
    const { onFrame } = await renderTeam();
    act(() => onFrame({ type: 'canvas_presence', project_id: 14,
      members: [{ actor_user_id: 3, email: 'a@x.com' }, { actor_user_id: 9, email: 'me@x.com' }] }));
    act(() => onFrame({ type: 'canvas_transient', project_id: 14, actor_user_id: 3,
      kind: 'node_editing', node_id: 'n1', editing: true }));
    expect(useCanvasPresenceStore.getState().members).toHaveLength(2);

    expect(lastCloseHandler).not.toBeNull();
    act(() => lastCloseHandler!());

    expect(useCanvasPresenceStore.getState().members).toEqual([]);
    expect(useCanvasPresenceStore.getState().editors).toEqual({});
    await waitFor(() => expect(document.querySelector('[data-presence-bar]')).toBeNull());
  });

  it('卸载时清空 presence（换项目不留一屏幻影）', async () => {
    const { page, onFrame } = await renderTeam();
    act(() => onFrame({ type: 'canvas_presence', project_id: 14,
      members: [{ actor_user_id: 3, email: 'a@x.com' }, { actor_user_id: 9, email: 'me@x.com' }] }));
    expect(useCanvasPresenceStore.getState().members).toHaveLength(2);

    page.unmount();
    expect(useCanvasPresenceStore.getState().members).toEqual([]);
  });

  // 个人项目零变化：不注册通道（发不出帧）、不渲染头像条，连收端都不认这两种帧。
  it('个人身份（观看态）：不注册通道、不收瞬态/presence、不渲染头像条', async () => {
    getAuthContexts.mockRejectedValue(new Error('500'));
    probeCanvasOwner.mockResolvedValue({ held: true, editorActorId: 42, isSelf: false, staleSeconds: 3 });
    renderPage();
    await waitFor(() => expect(lastFrameHandler).not.toBeNull());
    const onFrame = lastFrameHandler!;

    expect(isTransientChannelOpen()).toBe(false);
    expect(sendDragTransient([{ id: 'n1', x: 1, y: 2 }])).toBe(false);

    act(() => onFrame({ type: 'canvas_transient', project_id: 14, kind: 'drag',
      positions: [{ id: 'n1', x: 1, y: 2 }] }));
    act(() => onFrame({ type: 'canvas_transient', project_id: 14, kind: 'node_editing',
      node_id: 'n1', editing: true }));
    act(() => onFrame({ type: 'canvas_presence', project_id: 14,
      members: [{ actor_user_id: 3, email: 'a@x.com' }] }));

    expect(applyTransientPositions).not.toHaveBeenCalled();
    expect(useCanvasPresenceStore.getState().members).toEqual([]);
    expect(useCanvasPresenceStore.getState().editors).toEqual({});
    expect(document.querySelector('[data-presence-bar]')).toBeNull();
  });
});
