// 故事板「拖卡片 → 参考区」连线的拖拽数据协定。
//
// 用自定义 MIME 而不是 text/plain：参数区的 drop 目标必须能分辨「这是左侧
// 列表拖来的节点」还是别的东西（浏览器把拖入的文件/选中文字也走同一套
// dataTransfer）。自定义类型只有我们自己的 dragstart 会写，天然排他——
// 用户从桌面拖一张图进来不会被误认成连线请求（本地文件按约定不支持）。
export const STORYBOARD_NODE_MIME = 'application/x-tvmao-storyboard-node';

// ---- 全局「正在拖卡片」状态 -------------------------------------------------
// drop 目标（参数面板）要在「开始拖、还没拖到我上方」时就给灰虚线提示，
// 但 dragover 只有拖到元素上方才触发——面板自己感知不到别处的 dragstart。
// 所以由拖拽源在 dragstart/dragend 广播，这里用模块级状态 + 订阅，不进画布
// store（这是故事板的瞬时 UI 态，不该混进画布数据域，也不需要 undo/持久化）。
import { useSyncExternalStore } from 'react';

let dragActive = false;
const dragListeners = new Set<() => void>();

export function beginNodeDrag(): void {
  dragActive = true;
  dragListeners.forEach((f) => f());
}

/** dragend 在拖拽源上必触发（无论 drop 成功与否），清态挂它就不会漏。 */
export function endNodeDrag(): void {
  dragActive = false;
  dragListeners.forEach((f) => f());
}

export function isNodeDragActive(): boolean {
  return dragActive;
}

export function useNodeDragActive(): boolean {
  return useSyncExternalStore(
    (cb) => {
      dragListeners.add(cb);
      return () => dragListeners.delete(cb);
    },
    () => dragActive,
  );
}

export function setDragNodeId(dt: DataTransfer, nodeId: string): void {
  dt.setData(STORYBOARD_NODE_MIME, nodeId);
  dt.effectAllowed = 'link';
}

/** 不是我们的拖拽（没带这个 MIME）返回 null，调用方按「与我无关」处理。 */
export function getDragNodeId(dt: DataTransfer | null): string | null {
  if (!dt) return null;
  const id = dt.getData(STORYBOARD_NODE_MIME);
  return id || null;
}

/** dragover 阶段只能看 types（数据本体要到 drop 才可读，浏览器安全限制）。 */
export function hasDragNode(dt: DataTransfer | null): boolean {
  return !!dt && Array.from(dt.types).includes(STORYBOARD_NODE_MIME);
}
