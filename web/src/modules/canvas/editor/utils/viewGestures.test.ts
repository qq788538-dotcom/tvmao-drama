import { describe, expect, it } from 'vitest';
import {
  MAX_ZOOM,
  MIN_ZOOM,
  classifyWheel,
  fitView,
  panView,
  wheelZoomFactor,
  zoomViewAt,
} from './viewGestures';

describe('classifyWheel', () => {
  it('ctrlKey 但键盘 Ctrl 没按着 = 触摸板捏合', () => {
    expect(classifyWheel({ ctrlKey: true, metaKey: false }, false)).toBe('pinch');
  });

  it('ctrlKey 且键盘 Ctrl 真按着 = 修饰键缩放（Windows 鼠标通道）', () => {
    expect(classifyWheel({ ctrlKey: true, metaKey: false }, true)).toBe('zoom');
  });

  it('metaKey（⌘+滚轮）= 修饰键缩放', () => {
    expect(classifyWheel({ ctrlKey: false, metaKey: true }, false)).toBe('zoom');
  });

  it('无修饰键 = 平移', () => {
    expect(classifyWheel({ ctrlKey: false, metaKey: false }, false)).toBe('pan');
  });
});

describe('zoomViewAt', () => {
  it('锚点下方的世界坐标在缩放前后保持不动', () => {
    const view = { x: 100, y: 50, zoom: 1 };
    const next = zoomViewAt(view, 400, 300, 2);
    // 缩放前锚点对应世界坐标 (300, 250)；缩放后同一世界点应仍落在 (400, 300)
    expect(next.zoom).toBe(2);
    expect(400 - (400 - view.x) / view.zoom * next.zoom).toBe(next.x);
    expect(next.x).toBe(400 - 300 * 2);
    expect(next.y).toBe(300 - 250 * 2);
  });

  it('zoom 被夹在 MIN_ZOOM 与 MAX_ZOOM 之间', () => {
    const view = { x: 0, y: 0, zoom: 1 };
    expect(zoomViewAt(view, 0, 0, 100).zoom).toBe(MAX_ZOOM);
    expect(zoomViewAt(view, 0, 0, 0.001).zoom).toBe(MIN_ZOOM);
  });
});

describe('wheelZoomFactor', () => {
  it('deltaY 为负（向上/张开）放大，为正缩小', () => {
    expect(wheelZoomFactor(-10, 'pinch')).toBeGreaterThan(1);
    expect(wheelZoomFactor(10, 'pinch')).toBeLessThan(1);
    expect(wheelZoomFactor(-100, 'wheel')).toBeGreaterThan(1);
    expect(wheelZoomFactor(100, 'wheel')).toBeLessThan(1);
  });

  it('捏合比滚轮对同样 delta 更敏感（捏合事件的 delta 很小）', () => {
    expect(wheelZoomFactor(-10, 'pinch')).toBeGreaterThan(wheelZoomFactor(-10, 'wheel'));
  });
});

describe('panView', () => {
  it('内容跟随手指方向：view 平移量等于负的滚动 delta', () => {
    const view = { x: 100, y: 50, zoom: 1.5 };
    expect(panView(view, 30, -20)).toEqual({ x: 70, y: 70, zoom: 1.5 });
  });
});

describe('fitView', () => {
  it('空画布回到原点 100%', () => {
    expect(fitView([], 800, 600)).toEqual({ x: 0, y: 0, zoom: 1 });
  });

  it('所有节点包围盒缩放后完整落进视口', () => {
    const nodes = [
      { x: 0, y: 0, width: 400, height: 300 },
      { x: 1000, y: 800, width: 200, height: 100 },
    ];
    const view = fitView(nodes, 800, 600);
    // 包围盒 (0,0)-(1200,900)，四角变换到屏幕坐标后都应在视口内
    const corners = [
      { x: 0, y: 0 },
      { x: 1200, y: 900 },
    ];
    for (const c of corners) {
      const sx = c.x * view.zoom + view.x;
      const sy = c.y * view.zoom + view.y;
      expect(sx).toBeGreaterThanOrEqual(0);
      expect(sy).toBeGreaterThanOrEqual(0);
      expect(sx).toBeLessThanOrEqual(800);
      expect(sy).toBeLessThanOrEqual(600);
    }
  });

  it('小节点不会被放大超过 100%', () => {
    const view = fitView([{ x: 0, y: 0, width: 10, height: 10 }], 800, 600);
    expect(view.zoom).toBe(1);
    // 节点中心应落在视口中心
    expect(5 * view.zoom + view.x).toBeCloseTo(400);
    expect(5 * view.zoom + view.y).toBeCloseTo(300);
  });

  it('超大内容缩到下限为止', () => {
    const view = fitView([{ x: 0, y: 0, width: 100000, height: 100000 }], 800, 600);
    expect(view.zoom).toBe(MIN_ZOOM);
  });
});
