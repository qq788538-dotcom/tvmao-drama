import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeShell } from './NodeShell';
import { VideoPlayer } from './nodes/VideoPlayer';
import { stopIfInVideoControls } from './nodes/videoControlsGuard';
import { useCanvasStore } from './store';
import { setCanvasOwnerToken } from './persistence/canvasMutations';
import { patchCanvasNodePositions } from '@/shared/api';
import type { CanvasNode } from './types';
import { StoryboardModeContext } from '../storyboard/StoryboardContext';
import { stubFrames } from './utils/manualFrames.testing';

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

describe('NodeShell × 自绘视频控制条', () => {
  it('控制条上按下：节点被选中，事件不冒到画布（不拖动、不平移）', () => {
    const node = makeNode('video-custom-ctrl', 0);
    useCanvasStore.setState({ nodes: [node], selectedIds: new Set<string>() });
    const canvasMouseDown = vi.fn();
    render(
      <div onMouseDown={canvasMouseDown}>
        <NodeShell node={node}>
          <VideoPlayer src="https://example.com/a.mp4" />
        </NodeShell>
      </div>,
    );
    fireEvent.mouseDown(screen.getByRole('button', { name: '播放' }), { button: 0 });
    expect(canvasMouseDown).not.toHaveBeenCalled();
    expect(useCanvasStore.getState().selectedIds.has(node.id)).toBe(true);
  });
});

describe('NodeShell 拖拽落库', () => {
  function renderDraggable(id: string) {
    const node = makeNode(id, 0);
    useCanvasStore.setState({ nodes: [node], selectedIds: new Set<string>(), view: { x: 0, y: 0, zoom: 1 } });
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
    window.history.pushState({}, '', '/canvas/14');
    setCanvasOwnerToken('owner-token');
    vi.mocked(patchCanvasNodePositions).mockClear();
  });

  afterEach(() => {
    setCanvasOwnerToken('');
    vi.useRealTimers();
  });

  it('拖动后松手：一次 PATCH 落点坐标', async () => {
    const { shell } = renderDraggable('drag-up');
    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 40, clientY: 30 });
    fireEvent.mouseUp(window);

    expect(useCanvasStore.getState().nodes[0]).toMatchObject({ x: 40, y: 30 });
    await vi.waitFor(() => expect(patchCanvasNodePositions).toHaveBeenCalledTimes(1));
    expect(vi.mocked(patchCanvasNodePositions).mock.calls[0][2]).toEqual([
      expect.objectContaining({ id: 'drag-up', x: 40, y: 30 }),
    ]);
  });

  it('只点选没移动：不发 PATCH', async () => {
    const { shell } = renderDraggable('drag-click-only');
    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseUp(window);
    await vi.advanceTimersByTimeAsync(50);
    expect(patchCanvasNodePositions).not.toHaveBeenCalled();
  });

  // 切标签页/切应用时浏览器**不会**补 mouseup：按松手处理并照常落库。
  it('window blur 视同松手：照常落库，之后的 mousemove 不再改坐标', async () => {
    const { shell } = renderDraggable('drag-blur');
    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 10, clientY: 10 });

    fireEvent.blur(window);

    await vi.waitFor(() => expect(patchCanvasNodePositions).toHaveBeenCalledTimes(1));
    expect(vi.mocked(patchCanvasNodePositions).mock.calls[0][2]).toEqual([
      expect.objectContaining({ id: 'drag-blur' }),
    ]);
    const x = useCanvasStore.getState().nodes[0].x;
    fireEvent.mouseMove(window, { clientX: 90, clientY: 90 });
    expect(useCanvasStore.getState().nodes[0].x).toBe(x);
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

  it('多选拖拽：整批节点一起移动、一起落库', async () => {
    const a = makeNode('multi-a', 0);
    const b = makeNode('multi-b', 400);
    useCanvasStore.setState({ nodes: [a, b], selectedIds: new Set([a.id, b.id]), view: { x: 0, y: 0, zoom: 1 } });
    const { container } = render(
      <NodeShell node={a}>
        <div>body-a</div>
      </NodeShell>,
    );
    const shell = container.querySelector('[data-node-id="multi-a"]') as HTMLElement;

    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 10, clientY: 10 });
    fireEvent.mouseUp(window);

    await vi.waitFor(() => expect(patchCanvasNodePositions).toHaveBeenCalledTimes(1));
    const ids = vi.mocked(patchCanvasNodePositions).mock.calls[0][2].map((p: { id: string }) => p.id).sort();
    expect(ids).toEqual(['multi-a', 'multi-b']);
  });
});

// 拖动按帧合并（P5）：一帧内的多个 mousemove 只 set 一次 store；撤销点仍在第一次
// 移动时打下，松手先补交未到帧的位移再落库。
describe('NodeShell 拖动按帧合并', () => {
  let frames: ReturnType<typeof stubFrames>;
  const realMoveNodes = useCanvasStore.getState().moveNodes;

  beforeEach(() => {
    frames = stubFrames();
    window.history.pushState({}, '', '/canvas/14');
    setCanvasOwnerToken('owner-token');
    vi.mocked(patchCanvasNodePositions).mockClear();
  });

  afterEach(() => {
    useCanvasStore.setState({ moveNodes: realMoveNodes });
    setCanvasOwnerToken('');
    vi.unstubAllGlobals();
  });

  function renderDraggable(id: string) {
    const node = makeNode(id, 0);
    const moveNodes = vi.fn(realMoveNodes);
    useCanvasStore.setState({
      nodes: [node],
      selectedIds: new Set<string>(),
      view: { x: 0, y: 0, zoom: 2 },
      undoStack: [],
      redoStack: [],
      readOnly: false,
      moveNodes,
    });
    const view = render(
      <NodeShell node={node}>
        <div>body</div>
      </NodeShell>,
    );
    const shell = view.container.querySelector(`[data-node-id="${id}"]`) as HTMLElement;
    return { ...view, shell, moveNodes };
  }

  it('一帧内多次 mousemove 只提交一次，位移累加并按 zoom 换算', () => {
    const { shell, moveNodes } = renderDraggable('frame-a');
    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 10, clientY: 4 });
    fireEvent.mouseMove(window, { clientX: 30, clientY: 10 });
    fireEvent.mouseMove(window, { clientX: 40, clientY: 20 });

    // 帧还没到：store 不动，但撤销点已经打下（拖动前的状态）。
    expect(moveNodes).not.toHaveBeenCalled();
    expect(useCanvasStore.getState().undoStack).toHaveLength(1);

    act(() => frames.runFrame());
    expect(moveNodes).toHaveBeenCalledTimes(1);
    expect(useCanvasStore.getState().nodes[0]).toMatchObject({ x: 20, y: 10 });
    fireEvent.mouseUp(window);
  });

  it('松手先提交未到帧的位移，落库坐标是最终位置；撤销一步回到拖动前', async () => {
    const { shell, moveNodes } = renderDraggable('frame-b');
    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 20, clientY: 0 });
    act(() => frames.runFrame());
    fireEvent.mouseMove(window, { clientX: 60, clientY: 20 });
    fireEvent.mouseUp(window);

    expect(moveNodes).toHaveBeenCalledTimes(2);
    expect(useCanvasStore.getState().nodes[0]).toMatchObject({ x: 30, y: 10 });
    await vi.waitFor(() => expect(patchCanvasNodePositions).toHaveBeenCalledTimes(1));
    expect(vi.mocked(patchCanvasNodePositions).mock.calls[0][2]).toEqual([
      expect.objectContaining({ id: 'frame-b', x: 30, y: 10 }),
    ]);
    // 整次拖动只有一个撤销点。
    expect(useCanvasStore.getState().undoStack).toHaveLength(1);
    act(() => useCanvasStore.getState().undo());
    expect(useCanvasStore.getState().nodes[0]).toMatchObject({ x: 0, y: 0 });

    // 松手后迟到的帧不再改坐标。
    act(() => frames.runFrame());
    expect(moveNodes).toHaveBeenCalledTimes(2);
  });

  it('卸载时丢掉未提交的那一帧', () => {
    const { shell, moveNodes, unmount } = renderDraggable('frame-c');
    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 20, clientY: 0 });
    unmount();
    act(() => frames.runFrame());
    expect(moveNodes).not.toHaveBeenCalled();
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
