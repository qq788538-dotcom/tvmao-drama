// 外部（CLI）变更的整拉（CLI spec 第 6 节）：只在画布空闲时把服务端快照换进 store，
// 保留视口与选中，清空撤销栈（否则撤销会删掉外部新增的节点）。
import { getSnapshot } from '@/shared/api';
import { useCanvasStore } from '../store';
import type { CanvasSnapshot } from '../types';
import { canvasMutationSeq, isCanvasPersistenceIdle, seedCanvasBaselines } from './canvasMutations';

let pointerDown = false;

/** 全局跟踪"有按键按着"：节点拖动、缩放、平移、框选、连线都是 mousedown 后挂 window 监听的自研手势，统一用它判断。 */
export function installPointerTracker(win: Window = window): () => void {
  const down = () => { pointerDown = true; };
  const up = () => { pointerDown = false; };
  win.addEventListener('pointerdown', down, true);
  win.addEventListener('pointerup', up, true);
  win.addEventListener('pointercancel', up, true);
  win.addEventListener('blur', up);
  return () => {
    win.removeEventListener('pointerdown', down, true);
    win.removeEventListener('pointerup', up, true);
    win.removeEventListener('pointercancel', up, true);
    win.removeEventListener('blur', up);
    pointerDown = false;
  };
}

/** 与 CanvasEditor 快捷键里的判定一致：输入框、下拉、contenteditable（提示词编辑器）。 */
export function isEditingText(el: Element | null): boolean {
  if (!el) return false;
  const tag = el.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || (el as HTMLElement).isContentEditable === true;
}

export function isCanvasIdle(doc: Document = document): boolean {
  if (pointerDown) return false;
  if (isEditingText(doc.activeElement)) return false;
  if (useCanvasStore.getState().connecting) return false;
  return isCanvasPersistenceIdle();
}

/** 拉快照并换进 store，返回快照版本；拉取期间本地有新写入则丢弃结果并返回 null（调用方等空闲后重来）。 */
export async function reloadCanvasFromServer(pid: number): Promise<number | null> {
  const seq = canvasMutationSeq();
  const snap = await getSnapshot(pid);
  if (canvasMutationSeq() !== seq || !isCanvasPersistenceIdle()) return null;
  const raw = (snap.payload ?? {}) as Partial<CanvasSnapshot>;
  const nodes = raw.nodes ?? [];
  const s = useCanvasStore.getState();
  s.load({ nodes, connections: raw.connections ?? [], view: s.view }, { preserveUiState: true, resetHistory: true });
  // 基线必须对齐服务端，否则下一次 diff 会把旧值当成本地改动重发
  seedCanvasBaselines(nodes);
  return snap.version;
}
