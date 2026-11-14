import { act, render } from '@testing-library/react';
import { App as AntApp } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// 节点组件换成计数桩：本文件只测 CanvasEditor 是否把无关更新扇出到每个节点。
const renders = vi.hoisted(() => ({ count: new Map<string, number>() }));
vi.mock('./registry', () => {
  const Stub = ({ id }: { id: string }) => {
    renders.count.set(id, (renders.count.get(id) ?? 0) + 1);
    return <div data-node-id={id} />;
  };
  return {
    nodeComponents: new Proxy({}, { get: () => Stub }),
    nodeMenuItems: [],
    UPLOAD_MENU_TYPE: '__upload__',
  };
});

import { CanvasEditor } from './CanvasEditor';
import { useCanvasStore } from './store';
import { canvasViewKey, LOCAL_VIEW_SAVE_DEBOUNCE_MS } from './persistence/localView';
import { stubFrames } from './utils/manualFrames.testing';

beforeEach(() => {
  renders.count.clear();
  useCanvasStore.setState({
    nodes: [
      { id: 'n1', type: 'text-node', x: 0, y: 0, width: 100, height: 100 },
      { id: 'n2', type: 'text-node', x: 300, y: 0, width: 100, height: 100 },
    ],
    connections: [],
    view: { x: 0, y: 0, zoom: 1 },
    selectedIds: new Set<string>(),
    readOnly: false,
  });
});

// 回归：平移/缩放每帧改 view、拖动每帧改 nodes，CanvasEditor 重渲染时原先会把
// 全部节点组件整棵重跑；节点只该在自身订阅的数据变化时重渲染。
it('平移/缩放与拖动单个节点不会让其它节点组件重渲染', () => {
  render(
    <AntApp>
      <CanvasEditor />
    </AntApp>,
  );
  const base1 = renders.count.get('n1') ?? 0;
  const base2 = renders.count.get('n2') ?? 0;
  expect(base1).toBeGreaterThan(0);

  act(() => useCanvasStore.getState().setView({ x: 40, y: 20, zoom: 1.5 }));
  act(() => useCanvasStore.getState().moveNodes(['n1'], 10, 0));

  expect(renders.count.get('n1')).toBe(base1);
  expect(renders.count.get('n2')).toBe(base2);
});

// 滚轮按帧合并 + 本地视口去抖写入（P21）、paste 监听不随 view 反复注册（P32）。
describe('CanvasEditor 视口手势', () => {
  let frames: ReturnType<typeof stubFrames>;

  beforeEach(() => {
    frames = stubFrames();
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    window.localStorage.clear();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  function renderEditor() {
    const utils = render(
      <AntApp>
        <CanvasEditor projectId={14} />
      </AntApp>,
    );
    const viewport = utils.container.querySelector('[data-node-id]')!.closest('.absolute.left-0.top-0')!
      .parentElement as HTMLElement;
    return { ...utils, viewport };
  }

  function wheel(el: HTMLElement, deltaX: number, deltaY: number) {
    el.dispatchEvent(new WheelEvent('wheel', { deltaX, deltaY, bubbles: true, cancelable: true }));
  }

  it('同一帧内的多次滚轮平移累积后只 setView 一次', () => {
    const { viewport } = renderEditor();
    // 观察 view 的提交次数：每次 setView 都会换一个新的 view 对象。
    const seen: unknown[] = [];
    const unsub = useCanvasStore.subscribe((st, prev) => {
      if (st.view !== prev.view) seen.push(st.view);
    });

    wheel(viewport, 10, 0);
    wheel(viewport, 5, 20);
    wheel(viewport, 0, 30);
    expect(seen).toHaveLength(0);

    act(() => frames.runFrame());
    expect(seen).toEqual([{ x: -15, y: -50, zoom: 1 }]);
    unsub();
  });

  it('本地视口停手 300ms 后才写一次', () => {
    const { viewport } = renderEditor();
    const stored = () => window.localStorage.getItem(canvasViewKey(14));

    wheel(viewport, 10, 0);
    act(() => frames.runFrame());
    act(() => vi.advanceTimersByTime(LOCAL_VIEW_SAVE_DEBOUNCE_MS - 1));
    wheel(viewport, 10, 0);
    act(() => frames.runFrame());
    // 第二次滚动重新计时：此时距第一次已过 299ms，仍不该写。
    act(() => vi.advanceTimersByTime(LOCAL_VIEW_SAVE_DEBOUNCE_MS - 1));
    expect(stored()).toBeNull();

    act(() => vi.advanceTimersByTime(1));
    expect(JSON.parse(stored()!)).toEqual({ x: -20, y: 0, zoom: 1 });
  });

  it('卸载时把未到帧/去抖中的视口立即写入', () => {
    const { viewport, unmount } = renderEditor();
    wheel(viewport, 7, 0);
    unmount();
    expect(JSON.parse(window.localStorage.getItem(canvasViewKey(14))!)).toEqual({ x: -7, y: 0, zoom: 1 });
  });

  it('平移/缩放不会让 paste 监听反复摘挂', () => {
    const add = vi.spyOn(window, 'addEventListener');
    renderEditor();
    const pasteAdds = () => add.mock.calls.filter(([type]) => type === 'paste').length;
    const before = pasteAdds();
    expect(before).toBeGreaterThan(0);

    act(() => useCanvasStore.getState().setView({ x: 40, y: 20, zoom: 1.5 }));
    act(() => useCanvasStore.getState().setView({ x: 80, y: 20, zoom: 0.5 }));
    expect(pasteAdds()).toBe(before);
    add.mockRestore();
  });
});
