import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Minimap } from './Minimap';
import { useCanvasStore } from './store';

// P32：小地图每次重画都给 canvas 设 width/height，会清空位图并重建 backing store。
// 只在尺寸真的变化时设，缩放用 setTransform 设绝对值（不累乘）。
describe('Minimap 重画', () => {
  let widthSets: number;
  let setTransform: ReturnType<typeof vi.fn>;
  let scale: ReturnType<typeof vi.fn>;
  const widthDesc = Object.getOwnPropertyDescriptor(HTMLCanvasElement.prototype, 'width')!;

  beforeEach(() => {
    widthSets = 0;
    setTransform = vi.fn();
    scale = vi.fn();
    Object.defineProperty(HTMLCanvasElement.prototype, 'width', {
      configurable: true,
      get() {
        return widthDesc.get!.call(this);
      },
      set(v: number) {
        widthSets += 1;
        widthDesc.set!.call(this, v);
      },
    });
    const ctx = new Proxy(
      { setTransform, scale },
      { get: (target, key) => (key in target ? target[key as keyof typeof target] : () => {}) },
    );
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(ctx as never);
    useCanvasStore.setState({
      nodes: [{ id: 'a', type: 'gen-image', x: 0, y: 0, width: 100, height: 100 }],
      connections: [],
      view: { x: 0, y: 0, zoom: 1 },
    });
  });

  afterEach(() => {
    Object.defineProperty(HTMLCanvasElement.prototype, 'width', widthDesc);
    vi.restoreAllMocks();
  });

  it('平移/拖动重画时不重设 canvas 尺寸，变换按绝对值设置', () => {
    render(<Minimap />);
    expect(widthSets).toBe(1);

    act(() => useCanvasStore.getState().setView({ x: 30, y: 10, zoom: 1.2 }));
    act(() => useCanvasStore.getState().moveNodes(['a'], 5, 5));

    expect(widthSets).toBe(1);
    expect(setTransform.mock.calls.length).toBeGreaterThanOrEqual(3);
    const dpr = window.devicePixelRatio || 1;
    for (const call of setTransform.mock.calls) expect(call).toEqual([dpr, 0, 0, dpr, 0, 0]);
    expect(scale).not.toHaveBeenCalled();
  });
});
