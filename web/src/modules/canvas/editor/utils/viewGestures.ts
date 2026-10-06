import type { ViewState } from '../types';

// 画布缩放范围的唯一真源。历史上 Toolbar(0.25–2) 和滚轮(0.2–2.5) 各写了一套，
// 用户在两个入口会撞到不同的墙——统一取并集里较宽的那档。
export const MIN_ZOOM = 0.2;
export const MAX_ZOOM = 2.5;

export type WheelKind = 'pinch' | 'zoom' | 'pan';

/**
 * 把一个 wheel 事件归类成画布手势。
 *
 * 浏览器把触摸板双指捏合投递为 ctrlKey=true 的 wheel 事件，与「真按着 Ctrl
 * 滚滚轮」在事件字段上完全无法区分——唯一可靠信号是键盘上 Ctrl 是否真的处于
 * 按下状态（physicalCtrlHeld，由全局 gestureGuard 跟踪）。
 *
 * - pinch：捏合 → 缩放画布（以两指位置为锚点）
 * - zoom：⌘+滚轮（Mac）/ 真 Ctrl+滚轮（Windows 没有 ⌘，落到 Ctrl）→ 缩放画布
 * - pan：普通双指滚动 / 鼠标滚轮 → 平移画布
 */
export function classifyWheel(
  e: { ctrlKey: boolean; metaKey: boolean },
  physicalCtrlHeld: boolean,
): WheelKind {
  if (e.ctrlKey && !physicalCtrlHeld) return 'pinch';
  if (e.ctrlKey || e.metaKey) return 'zoom';
  return 'pan';
}

/** 以视口内 (anchorX, anchorY) 为锚点缩放：锚点下方的世界坐标缩放前后不动。 */
export function zoomViewAt(
  view: ViewState,
  anchorX: number,
  anchorY: number,
  nextZoomRaw: number,
): ViewState {
  const nextZoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, nextZoomRaw));
  const worldX = (anchorX - view.x) / view.zoom;
  const worldY = (anchorY - view.y) / view.zoom;
  return {
    x: anchorX - worldX * nextZoom,
    y: anchorY - worldY * nextZoom,
    zoom: nextZoom,
  };
}

/**
 * deltaY → 缩放倍率。捏合手势的 delta 是连续小值（±1~10），滚轮是离散大值
 * （±100 左右），同一条曲线两头必有一头手感失调，所以分开调参。
 */
export function wheelZoomFactor(deltaY: number, mode: 'pinch' | 'wheel'): number {
  if (mode === 'pinch') return Math.exp(-deltaY * 0.01);
  return 1 - deltaY * 0.0015;
}

/** 双指滚动平移：内容跟随手指方向移动（与滚动网页同向）。 */
export function panView(view: ViewState, deltaX: number, deltaY: number): ViewState {
  return { x: view.x - deltaX, y: view.y - deltaY, zoom: view.zoom };
}

const FIT_PADDING = 80;

/** ⌘0 适应画布：缩放+平移到所有节点刚好可见。上限 100%——单个小节点不该被吹大到糊。 */
export function fitView(
  nodes: Array<{ x: number; y: number; width: number; height: number }>,
  viewportW: number,
  viewportH: number,
): ViewState {
  if (nodes.length === 0) return { x: 0, y: 0, zoom: 1 };
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const n of nodes) {
    if (n.x < minX) minX = n.x;
    if (n.y < minY) minY = n.y;
    if (n.x + n.width > maxX) maxX = n.x + n.width;
    if (n.y + n.height > maxY) maxY = n.y + n.height;
  }
  const bw = maxX - minX;
  const bh = maxY - minY;
  const zoomRaw = Math.min(
    1,
    (viewportW - FIT_PADDING * 2) / Math.max(1, bw),
    (viewportH - FIT_PADDING * 2) / Math.max(1, bh),
  );
  const zoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, zoomRaw));
  return {
    x: viewportW / 2 - (minX + bw / 2) * zoom,
    y: viewportH / 2 - (minY + bh / 2) * zoom,
    zoom,
  };
}
