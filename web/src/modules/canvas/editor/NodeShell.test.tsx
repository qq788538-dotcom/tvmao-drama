import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeShell } from './NodeShell';
import { stopIfInVideoControls } from './nodes/videoControlsGuard';
import { clearTransientPositions, useCanvasStore } from './store';
import { isNodeInteracting, resetInteractionRegistry } from './interactionRegistry';
import {
  configureCanvasPersistence,
  resetCanvasPersistenceForTest,
  setCanvasOwnerToken,
} from './persistence/canvasMutations';
import { patchCanvasNodePositions } from '@/shared/api';
import { resetCanvasPresence, useCanvasPresenceStore } from './presenceStore';
import { setTransientChannel, type OutgoingTransientFrame } from './transientChannel';
import type { CanvasNode } from './types';
import { StoryboardModeContext } from '../storyboard/StoryboardContext';

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return { ...actual, patchCanvasNodePositions: vi.fn(async () => ({ version: 2 })) };
});

function makeNode(id: string, x: number): CanvasNode {
  return {
    id,
    type: 'gen-image',
    x,
    y: 0,
    width: 280,
    height: 280,
    settings: {},
    status: 'idle',
  };
}

describe('NodeShell stacking', () => {
  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [],
      selectedIds: new Set<string>(),
    });
  });

  it('puts selected nodes above ordinary sibling nodes', () => {
    const selected = makeNode('selected-node', 0);
    const ordinary = makeNode('ordinary-node', 140);
    useCanvasStore.setState({
      nodes: [selected, ordinary],
      selectedIds: new Set([selected.id]),
    });

    render(
      <>
        <NodeShell node={selected}>
          <div>selected body</div>
        </NodeShell>
        <NodeShell node={ordinary}>
          <div>ordinary body</div>
        </NodeShell>
      </>,
    );

    const selectedShell = screen.getByText('selected body').closest('[data-node-id]') as HTMLElement;
    const ordinaryShell = screen.getByText('ordinary body').closest('[data-node-id]') as HTMLElement;

    expect(Number(selectedShell.style.zIndex)).toBeGreaterThan(Number(ordinaryShell.style.zIndex));
  });

  it('selects the node when a child video consumes mouse down bubbling', () => {
    const node = makeNode('video-node', 0);
    useCanvasStore.setState({
      nodes: [node],
      selectedIds: new Set<string>(),
    });

    render(
      <NodeShell node={node}>
        <video aria-label="generated video" onMouseDown={(e) => e.stopPropagation()} />
      </NodeShell>,
    );

    fireEvent.mouseDown(screen.getByLabelText('generated video'), { button: 0 });

    expect(useCanvasStore.getState().selectedIds.has(node.id)).toBe(true);
  });
});

describe('NodeShell 右键删除（兜底菜单）', () => {
  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [],
      selectedIds: new Set<string>(),
    });
  });

  it('右键节点弹出菜单，点「删除」移除节点', () => {
    const node = makeNode('ctx-node', 0);
    useCanvasStore.setState({ nodes: [node] });

    render(
      <NodeShell node={node}>
        <div>body</div>
      </NodeShell>,
    );

    const shell = screen.getByText('body').closest('[data-node-id]') as HTMLElement;
    fireEvent.contextMenu(shell, { clientX: 10, clientY: 10 });

    fireEvent.click(screen.getByText('删除'));
    expect(useCanvasStore.getState().nodes.find((n) => n.id === node.id)).toBeUndefined();
  });

  it('右键输入类元素放行原生菜单，不弹壳菜单', () => {
    const node = makeNode('input-node', 0);
    useCanvasStore.setState({ nodes: [node] });

    render(
      <NodeShell node={node}>
        <textarea aria-label="prompt-area" />
      </NodeShell>,
    );

    fireEvent.contextMenu(screen.getByLabelText('prompt-area'), { clientX: 10, clientY: 10 });
    expect(screen.queryByText('删除')).toBeNull();
  });
});

describe('NodeShell 视频拖动与画布平移的边界', () => {
  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [],
      selectedIds: new Set<string>(),
    });
  });

  // 复现生产 bug：视频画面区域的 mousedown 曾被捕获兜底标记成「已处理」，
  // 冒泡入口早退又没 stopPropagation，事件冒到画布根触发视图平移——表现为
  // 按住视频拖动时所有节点一起动。
  function renderVideoInShell(nodeId: string) {
    const node = makeNode(nodeId, 0);
    useCanvasStore.setState({ nodes: [node] });
    const canvasMouseDown = vi.fn();
    render(
      <div onMouseDown={canvasMouseDown}>
        <NodeShell node={node}>
          <video aria-label="gen video" controls onMouseDown={stopIfInVideoControls} />
        </NodeShell>
      </div>,
    );
    const video = screen.getByLabelText('gen video') as HTMLVideoElement;
    // jsdom 无布局，手工给几何：布局高 400，屏幕同高（zoom=1）。
    Object.defineProperty(video, 'clientHeight', { value: 400, configurable: true });
    video.getBoundingClientRect = () =>
      ({ top: 0, left: 0, right: 300, bottom: 400, width: 300, height: 400, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect;
    return { node, video, canvasMouseDown };
  }

  it('画面区域按下：NodeShell 启动拖动并拦断冒泡，画布层收不到（不会平移）', () => {
    const { video, canvasMouseDown } = renderVideoInShell('video-pic');
    fireEvent.mouseDown(video, { button: 0, clientY: 100 });
    expect(canvasMouseDown).not.toHaveBeenCalled();
    fireEvent.mouseUp(window);
  });

  it('控制条区域按下：视频自己拦断冒泡，节点被选中但不开始拖动', () => {
    const { node, video, canvasMouseDown } = renderVideoInShell('video-ctrl');
    fireEvent.mouseDown(video, { button: 0, clientY: 390 });
    expect(canvasMouseDown).not.toHaveBeenCalled();
    expect(useCanvasStore.getState().selectedIds.has(node.id)).toBe(true);
  });
});

describe('NodeShell 拖拽瞬态流（团队并发编辑）', () => {
  let sent: OutgoingTransientFrame[];

  function renderDraggable(id: string) {
    const node = makeNode(id, 0);
    useCanvasStore.setState({ nodes: [node], selectedIds: new Set<string>() });
    const view = render(
      <NodeShell node={node}>
        <div>body</div>
      </NodeShell>,
    );
    const shell = view.container.querySelector(`[data-node-id="${id}"]`) as HTMLElement;
    return { ...view, shell };
  }

  beforeEach(() => {
    vi.useFakeTimers();
    resetInteractionRegistry();
    sent = [];
    setTransientChannel({ projectId: 14, send: (f) => { sent.push(f); return true; } });
  });

  afterEach(() => {
    setTransientChannel(null);
    resetInteractionRegistry();
    vi.useRealTimers();
  });

  // 不节流的话一次两秒的拖拽会发上百帧，每帧还要在服务端扇出给项目下所有连接。
  it('mousemove 按 200ms 节流：窗口内连发多次只出一帧，跨过窗口再出一帧', () => {
    const { shell } = renderDraggable('drag-throttle');

    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 10, clientY: 10 });
    vi.advanceTimersByTime(50);
    fireEvent.mouseMove(window, { clientX: 20, clientY: 20 });
    vi.advanceTimersByTime(50);
    fireEvent.mouseMove(window, { clientX: 30, clientY: 30 });
    expect(sent).toHaveLength(1);

    vi.advanceTimersByTime(200);
    fireEvent.mouseMove(window, { clientX: 40, clientY: 40 });
    expect(sent).toHaveLength(2);
    // 发的是绝对坐标（丢帧不需要补偿，下一帧直接覆盖）。
    expect(sent[1]).toMatchObject({ type: 'canvas_transient', project_id: 14, kind: 'drag' });
    expect(sent[1].positions).toEqual([{ id: 'drag-throttle', x: 40, y: 40 }]);

    fireEvent.mouseUp(window);
  });

  it('松手后不再发瞬态（后续由权威 patchPositions 定格）', () => {
    const { shell } = renderDraggable('drag-stop');

    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 10, clientY: 10 });
    fireEvent.mouseUp(window);
    const afterUp = sent.length;

    vi.advanceTimersByTime(400);
    fireEvent.mouseMove(window, { clientX: 90, clientY: 90 });
    expect(sent).toHaveLength(afterUp);
  });

  // 通道只在团队项目注册；个人项目的拖拽必须一帧都不发。
  it('个人项目（通道未注册）拖拽零帧', () => {
    setTransientChannel(null);
    const { shell } = renderDraggable('drag-personal');

    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 10, clientY: 10 });
    vi.advanceTimersByTime(300);
    fireEvent.mouseMove(window, { clientX: 20, clientY: 20 });
    fireEvent.mouseUp(window);

    expect(sent).toHaveLength(0);
  });

  // begin/end 漏配对的后果：该节点上挂起的远端 op 永远不落地，队友之后对它的
  // 每一次改动在本端静默消失。
  it('拖拽期间登记在途，松手解除', () => {
    const { shell } = renderDraggable('drag-interaction');

    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    expect(isNodeInteracting('drag-interaction')).toBe(true);
    fireEvent.mouseMove(window, { clientX: 10, clientY: 10 });
    fireEvent.mouseUp(window);
    expect(isNodeInteracting('drag-interaction')).toBe(false);
  });

  // 真实事件序（审查 I1）：拖拽期间收到队友的权威坐标 → 松手。
  // 曾经的写法是「先解除在途登记（立刻 flush 挂起的远端坐标）→ 再读几何落库」，
  // 于是松手瞬间节点跳到队友的坐标，而且**这个坐标**被当成我的移动 PATCH 回服务端：
  // 拖拽期间辛苦挂起的保护在最后一步被自己丢掉。
  it('拖拽中收到远端坐标：松手保住我的落点，PATCH 发的也是落点', async () => {
    window.history.pushState({}, '', '/canvas/14');
    configureCanvasPersistence({ projectID: 14 });
    setCanvasOwnerToken('owner-token');
    vi.mocked(patchCanvasNodePositions).mockClear();

    const { shell } = renderDraggable('drag-vs-remote');
    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 40, clientY: 30 });

    // 队友把同一个节点挪到别处（权威 op，非瞬态）。拖拽中 → 挂起。
    useCanvasStore.getState().applyRemoteOp({
      kind: 'patchPositions',
      positions: [{ id: 'drag-vs-remote', x: 900, y: 900 }],
    });
    expect(useCanvasStore.getState().nodes[0]).toMatchObject({ x: 40, y: 30 });

    fireEvent.mouseUp(window);

    // 落点保住（挂起帧 flush 时几何字段被在飞登记剔掉）。
    expect(useCanvasStore.getState().nodes[0]).toMatchObject({ x: 40, y: 30 });
    await vi.waitFor(() => expect(patchCanvasNodePositions).toHaveBeenCalledTimes(1));
    expect(vi.mocked(patchCanvasNodePositions).mock.calls[0][2]).toEqual([
      expect.objectContaining({ id: 'drag-vs-remote', x: 40, y: 30 }),
    ]);

    resetCanvasPersistenceForTest();
  });

  it('只点选没移动也解除登记（那条早退路径同样要收尾）', () => {
    const { shell } = renderDraggable('drag-click-only');

    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseUp(window);
    expect(isNodeInteracting('drag-click-only')).toBe(false);
  });

  it('拖拽中组件卸载：登记也解除（mouseup 永远不会来的那条路）', () => {
    const { shell, unmount } = renderDraggable('drag-unmount');

    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 10, clientY: 10 });
    expect(isNodeInteracting('drag-unmount')).toBe(true);
    unmount();
    expect(isNodeInteracting('drag-unmount')).toBe(false);
  });

  // 第三条退出路径：切标签页/切应用时浏览器**不会**补 mouseup。没有它，拖拽状态
  // 一直挂着——在途登记不解除（挂起的远端 op 永久卡住）、瞬态还在发、切回来一动
  // 鼠标节点就接着跟手飘。
  it('window blur 视同松手：停发瞬态、解除在途、照常落库', async () => {
    window.history.pushState({}, '', '/canvas/14');
    setCanvasOwnerToken('owner-token');
    vi.mocked(patchCanvasNodePositions).mockClear();

    const { shell } = renderDraggable('drag-blur');
    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 10, clientY: 10 });
    const beforeBlur = sent.length;

    fireEvent.blur(window);
    expect(isNodeInteracting('drag-blur')).toBe(false);

    // 落库：用户切窗前看到的就是这个位置，丢掉等于回滚他刚做的移动。
    await vi.waitFor(() => expect(patchCanvasNodePositions).toHaveBeenCalledTimes(1));
    expect(vi.mocked(patchCanvasNodePositions).mock.calls[0][2]).toEqual([
      expect.objectContaining({ id: 'drag-blur' }),
    ]);

    // 监听已摘：blur 之后的 mousemove 不再产生瞬态帧。
    vi.advanceTimersByTime(400);
    fireEvent.mouseMove(window, { clientX: 90, clientY: 90 });
    expect(sent).toHaveLength(beforeBlur);
    setCanvasOwnerToken('');
  });

  it('卸载摘掉 window 监听：之后的 mousemove 不再改节点坐标', () => {
    const { shell, unmount } = renderDraggable('drag-detach');
    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 10, clientY: 10 });
    const movedTo = useCanvasStore.getState().nodes.find((n) => n.id === 'drag-detach')!.x;

    unmount();
    fireEvent.mouseMove(window, { clientX: 400, clientY: 400 });
    expect(useCanvasStore.getState().nodes.find((n) => n.id === 'drag-detach')!.x).toBe(movedTo);
  });

  it('多选拖拽：整批节点一起登记、一起广播', () => {
    const a = makeNode('multi-a', 0);
    const b = makeNode('multi-b', 400);
    useCanvasStore.setState({ nodes: [a, b], selectedIds: new Set([a.id, b.id]) });
    const { container } = render(
      <NodeShell node={a}>
        <div>body-a</div>
      </NodeShell>,
    );
    const shell = container.querySelector('[data-node-id="multi-a"]') as HTMLElement;

    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    expect(isNodeInteracting('multi-a')).toBe(true);
    expect(isNodeInteracting('multi-b')).toBe(true);
    fireEvent.mouseMove(window, { clientX: 10, clientY: 10 });
    expect(sent[0].positions?.map((p) => p.id).sort()).toEqual(['multi-a', 'multi-b']);
    fireEvent.mouseUp(window);
    expect(isNodeInteracting('multi-b')).toBe(false);
  });
});

describe('NodeShell 瞬态坐标覆盖层', () => {
  beforeEach(() => {
    clearTransientPositions();
    useCanvasStore.setState({ nodes: [], selectedIds: new Set<string>(), transientPositions: {} });
  });
  afterEach(() => act(() => clearTransientPositions()));

  function renderAt(id: string) {
    const node = makeNode(id, 120);
    node.y = 60;
    useCanvasStore.setState({ nodes: [node], selectedIds: new Set<string>() });
    const view = render(
      <NodeShell node={node}>
        <div>body</div>
      </NodeShell>,
    );
    return { ...view, shell: view.container.querySelector(`[data-node-id="${id}"]`) as HTMLElement };
  }

  it('有覆盖条目时画在覆盖坐标上', () => {
    const { shell } = renderAt('ov-1');
    expect(shell.style.left).toBe('120px');

    act(() => useCanvasStore.getState().applyTransientPositions([{ id: 'ov-1', x: 300, y: 400 }]));
    expect(shell.style.left).toBe('300px');
    expect(shell.style.top).toBe('400px');
    // 覆盖层与 node 本体是两层：节点自身坐标一动不动（它才是持久化 diff 的面）。
    expect(useCanvasStore.getState().nodes[0]).toMatchObject({ x: 120, y: 60 });
  });

  it('覆盖条目清掉后位置回落到节点自身坐标', () => {
    const { shell } = renderAt('ov-2');
    act(() => useCanvasStore.getState().applyTransientPositions([{ id: 'ov-2', x: 300, y: 400 }]));
    expect(shell.style.left).toBe('300px');

    act(() => clearTransientPositions());
    expect(shell.style.left).toBe('120px');
    expect(shell.style.top).toBe('60px');
  });
});

describe('NodeShell 「正在编辑」角标', () => {
  beforeEach(() => {
    resetCanvasPresence();
    useCanvasStore.setState({ nodes: [], selectedIds: new Set<string>() });
  });
  // reset 会通知还挂载着的 NodeShell 重渲染（角标消失），得在 act 里做。
  afterEach(() => act(() => resetCanvasPresence()));

  it('队友在编辑该节点时显示邮箱角标', () => {
    const node = makeNode('badge-node', 0);
    useCanvasStore.setState({ nodes: [node] });
    useCanvasPresenceStore.getState().setMembers([{ actorUserId: 7, email: 'mate@x.com' }]);
    useCanvasPresenceStore.getState().noteNodeEditing('badge-node', 7, true);

    render(
      <NodeShell node={node}>
        <div>body</div>
      </NodeShell>,
    );

    expect(screen.getByText('mate@x.com 正在编辑')).toBeInTheDocument();
  });

  // 拍板：只提示不阻止——字段级 LWW 下后提交者胜，硬锁只会把人挡在门外。
  it('角标不阻止本地交互：节点照样能选中、能拖', () => {
    const node = makeNode('badge-open', 0);
    useCanvasStore.setState({ nodes: [node], selectedIds: new Set<string>() });
    useCanvasPresenceStore.getState().noteNodeEditing('badge-open', 7, true);

    const { container } = render(
      <NodeShell node={node}>
        <div>body</div>
      </NodeShell>,
    );
    const shell = container.querySelector('[data-node-id="badge-open"]') as HTMLElement;

    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 30, clientY: 0 });
    fireEvent.mouseUp(window);

    expect(useCanvasStore.getState().selectedIds.has('badge-open')).toBe(true);
    expect(useCanvasStore.getState().nodes.find((n) => n.id === 'badge-open')!.x).toBeGreaterThan(0);
  });

  it('没人在编辑就没有角标', () => {
    const node = makeNode('badge-none', 0);
    useCanvasStore.setState({ nodes: [node] });
    render(
      <NodeShell node={node}>
        <div>body</div>
      </NodeShell>,
    );
    expect(screen.queryByText(/正在编辑/)).toBeNull();
  });
});

describe('NodeShell 故事板模式', () => {
  beforeEach(() => {
    useCanvasStore.setState({ nodes: [], selectedIds: new Set<string>() });
  });

  function renderInStoryboard(node: CanvasNode) {
    useCanvasStore.setState({ nodes: [node], selectedIds: new Set<string>() });
    return render(
      <StoryboardModeContext.Provider value={true}>
        <NodeShell node={node}>
          <div data-testid="body">内容</div>
        </NodeShell>
      </StoryboardModeContext.Provider>,
    );
  }

  it('不按画布坐标绝对定位，改为铺满容器', () => {
    const node = makeNode('sb-node', 999);
    node.y = 888;
    const { container } = renderInStoryboard(node);
    const wrapper = container.querySelector('[data-node-id="sb-node"]') as HTMLElement;

    expect(wrapper.className).not.toContain('absolute');
    expect(wrapper.className).toContain('relative');
    expect(wrapper.style.left).toBe('');
    expect(wrapper.style.top).toBe('');
    expect(wrapper.style.width).toBe('100%');
    expect(wrapper.style.height).toBe('100%');
  });

  // 故事板里没有画布坐标系,拖动无处可去;而 moveNodes 会真的改 node.x/y 并
  // PATCH 到后端——不禁掉的话在故事板里划一下就把画布布局改了。
  it('禁用拖拽：mousedown 不会调用 moveNodes', () => {
    const moveNodes = vi.fn();
    const node = makeNode('sb-drag', 0);
    useCanvasStore.setState({ nodes: [node], selectedIds: new Set<string>(), moveNodes } as never);
    const { container } = renderInStoryboard(node);
    const wrapper = container.querySelector('[data-node-id="sb-drag"]') as HTMLElement;

    fireEvent.mouseDown(wrapper, { button: 0, clientX: 10, clientY: 10 });
    fireEvent.mouseMove(window, { clientX: 80, clientY: 80 });
    fireEvent.mouseUp(window);

    expect(moveNodes).not.toHaveBeenCalled();
  });

  it('不渲染输入/输出连接端口', () => {
    const node = makeNode('sb-ports', 0);
    const { container } = renderInStoryboard(node);
    expect(container.querySelectorAll('[data-port-type]').length).toBe(0);
  });

  it('画布模式下一切照旧：仍然绝对定位在 node.x / node.y', () => {
    const node = makeNode('canvas-node', 120);
    node.y = 60;
    useCanvasStore.setState({ nodes: [node], selectedIds: new Set<string>() });
    const { container } = render(
      <NodeShell node={node}>
        <div>内容</div>
      </NodeShell>,
    );
    const wrapper = container.querySelector('[data-node-id="canvas-node"]') as HTMLElement;

    expect(wrapper.className).toContain('absolute');
    expect(wrapper.style.left).toBe('120px');
    expect(wrapper.style.top).toBe('60px');
    expect(wrapper.style.width).toBe('280px');
    expect(wrapper.style.height).toBe('280px');
    expect(wrapper.style.zIndex).toBe('1');
  });
});
