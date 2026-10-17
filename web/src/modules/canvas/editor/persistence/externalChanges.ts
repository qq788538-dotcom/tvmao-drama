// 外部（CLI）变更的整拉：只在画布空闲时把服务端快照换进 store，
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
  // 窗口失焦时 activeElement 不会变（WKWebView 里点按钮也不移走焦点）：用户切到终端看 AI 改画布时
  // 输入框仍是 activeElement，但他并没有在打字，不能因此一直不刷新。
  if (doc.hasFocus() && isEditingText(doc.activeElement)) return false;
  if (useCanvasStore.getState().connecting) return false;
  return isCanvasPersistenceIdle();
}

/** 拉快照并换进 store，返回快照版本；拉取期间本地有新写入或画布不再空闲，则丢弃结果并返回 null（调用方等空闲后重来）。
 *  isCurrent：调用方的订阅是否仍有效。拉取期间切到了别的项目时，旧项目的快照绝不能
 *  换进 store——那会让新项目的画布显示旧项目节点，基线也被播成旧项目的，之后的编辑
 *  会按新项目落库。 */
export async function reloadCanvasFromServer(
  pid: number,
  isCurrent: () => boolean = () => true,
): Promise<number | null> {
  const seq = canvasMutationSeq();
  const snap = await getSnapshot(pid);
  if (!isCurrent()) return null;
  // 拉取期间用户可能开始拖动/打字：此时换 store 会打断操作，交给调用方稍后重试
  if (canvasMutationSeq() !== seq || !isCanvasIdle()) return null;
  const raw = snap.payload as Partial<CanvasSnapshot> | null | undefined;
  const nodes = Array.isArray(raw?.nodes) ? raw!.nodes : [];
  const connections = Array.isArray(raw?.connections) ? raw!.connections : [];
  const s = useCanvasStore.getState();
  s.load({ nodes, connections, view: s.view }, { preserveUiState: true, resetHistory: true });
  // 基线必须对齐服务端，否则下一次 diff 会把旧值当成本地改动重发
  seedCanvasBaselines(nodes);
  return snap.version;
}
