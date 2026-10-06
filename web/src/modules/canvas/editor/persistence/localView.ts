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
