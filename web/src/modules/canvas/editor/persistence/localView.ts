import type { ViewState } from '../types';

function fallbackView(): ViewState {
  return { x: 0, y: 0, zoom: 1 };
}

export function canvasViewKey(projectID: number): string {
  return `tvmao:canvas:view:${projectID}`;
}

export function loadLocalView(projectID: number): ViewState {
  try {
    const raw = window.localStorage.getItem(canvasViewKey(projectID));
    if (!raw) return fallbackView();

    const parsed = JSON.parse(raw) as Partial<ViewState>;
    if (
      typeof parsed.x !== 'number'
      || typeof parsed.y !== 'number'
      || typeof parsed.zoom !== 'number'
      || !Number.isFinite(parsed.x)
      || !Number.isFinite(parsed.y)
      || !Number.isFinite(parsed.zoom)
    ) {
      return fallbackView();
    }

    return {
      x: parsed.x,
      y: parsed.y,
      zoom: Math.min(2.5, Math.max(0.2, parsed.zoom)),
    };
  } catch {
    return fallbackView();
  }
}

export function saveLocalView(projectID: number, view: ViewState): void {
  try {
    window.localStorage.setItem(canvasViewKey(projectID), JSON.stringify(view));
  } catch {
    // localStorage may be unavailable in private browsing or quota pressure.
  }
}

// 视口写本地存储的去抖窗口。滚轮/触控板平移缩放一秒能触发上百次，
// localStorage.setItem 是同步 I/O，每次都写会卡主线程（P21）。停 300ms 再写一次。
export const LOCAL_VIEW_SAVE_DEBOUNCE_MS = 300;

let pendingSave: { projectID: number; view: ViewState } | null = null;
let pendingSaveTimer: ReturnType<typeof setTimeout> | null = null;

/** 去抖版 saveLocalView：窗口内只保留最后一次视口，停手后写一次。 */
export function scheduleSaveLocalView(projectID: number, view: ViewState): void {
  // 换项目时先把上一个项目的待写视口落下，不能被新项目的写入顶掉。
  if (pendingSave && pendingSave.projectID !== projectID) flushLocalView();
  pendingSave = { projectID, view };
  if (pendingSaveTimer !== null) clearTimeout(pendingSaveTimer);
  pendingSaveTimer = setTimeout(flushLocalView, LOCAL_VIEW_SAVE_DEBOUNCE_MS);
}

/** 立即写入还在去抖中的视口。画布卸载时调用，避免最后一次平移/缩放丢失。 */
export function flushLocalView(): void {
  if (pendingSaveTimer !== null) {
    clearTimeout(pendingSaveTimer);
    pendingSaveTimer = null;
  }
  const pending = pendingSave;
  pendingSave = null;
  if (pending) saveLocalView(pending.projectID, pending.view);
}
