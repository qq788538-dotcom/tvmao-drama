import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  canvasViewKey,
  flushLocalView,
  loadLocalView,
  LOCAL_VIEW_SAVE_DEBOUNCE_MS,
  saveLocalView,
  scheduleSaveLocalView,
} from './localView';

describe('localView', () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.restoreAllMocks();
  });

  it('formats canvas view storage keys by project ID', () => {
    expect(canvasViewKey(14)).toBe('tvmao:canvas:view:14');
  });

  it('falls back when storage is missing or JSON is invalid', () => {
    expect(loadLocalView(14)).toEqual({ x: 0, y: 0, zoom: 1 });

    window.localStorage.setItem(canvasViewKey(14), '{bad json');

    expect(loadLocalView(14)).toEqual({ x: 0, y: 0, zoom: 1 });
  });

  it('falls back when required fields are missing or non-finite', () => {
    window.localStorage.setItem(canvasViewKey(14), JSON.stringify({ x: 1, y: 2 }));
    expect(loadLocalView(14)).toEqual({ x: 0, y: 0, zoom: 1 });

    window.localStorage.setItem(canvasViewKey(14), '{"x":1e999,"y":2,"zoom":1}');
    expect(loadLocalView(14)).toEqual({ x: 0, y: 0, zoom: 1 });

    window.localStorage.setItem(canvasViewKey(14), '{"x":1,"y":-1e999,"zoom":1}');
    expect(loadLocalView(14)).toEqual({ x: 0, y: 0, zoom: 1 });

    window.localStorage.setItem(canvasViewKey(14), '{"x":1,"y":2,"zoom":1e999}');
    expect(loadLocalView(14)).toEqual({ x: 0, y: 0, zoom: 1 });
  });

  it('clamps zoom while preserving valid x and y', () => {
    window.localStorage.setItem(canvasViewKey(14), JSON.stringify({ x: 8, y: -9, zoom: 0.1 }));
    expect(loadLocalView(14)).toEqual({ x: 8, y: -9, zoom: 0.2 });

    window.localStorage.setItem(canvasViewKey(14), JSON.stringify({ x: 8, y: -9, zoom: 3 }));
    expect(loadLocalView(14)).toEqual({ x: 8, y: -9, zoom: 2.5 });
  });

  it('saves view state as JSON', () => {
    saveLocalView(14, { x: 1, y: 2, zoom: 1.5 });

    expect(window.localStorage.getItem(canvasViewKey(14))).toBe(JSON.stringify({
      x: 1,
      y: 2,
      zoom: 1.5,
    }));
  });

  it('handles storage exceptions when loading and saving', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });
    expect(loadLocalView(14)).toEqual({ x: 0, y: 0, zoom: 1 });

    vi.restoreAllMocks();
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('quota exceeded');
    });
    expect(() => saveLocalView(14, { x: 1, y: 2, zoom: 1 })).not.toThrow();
  });

  describe('scheduleSaveLocalView 去抖', () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });
    afterEach(() => {
      flushLocalView();
      vi.useRealTimers();
    });

    it('窗口内多次调用只在停手后写最后一次', () => {
      scheduleSaveLocalView(14, { x: 1, y: 0, zoom: 1 });
      vi.advanceTimersByTime(LOCAL_VIEW_SAVE_DEBOUNCE_MS - 1);
      scheduleSaveLocalView(14, { x: 2, y: 0, zoom: 1 });
      vi.advanceTimersByTime(LOCAL_VIEW_SAVE_DEBOUNCE_MS - 1);
      expect(window.localStorage.getItem(canvasViewKey(14))).toBeNull();

      vi.advanceTimersByTime(1);
      expect(loadLocalView(14)).toEqual({ x: 2, y: 0, zoom: 1 });
    });

    it('flushLocalView 立即写入去抖中的视口', () => {
      scheduleSaveLocalView(14, { x: 3, y: 4, zoom: 1 });
      flushLocalView();
      expect(loadLocalView(14)).toEqual({ x: 3, y: 4, zoom: 1 });
    });

    it('换项目时先落下上一个项目的待写视口', () => {
      scheduleSaveLocalView(14, { x: 5, y: 0, zoom: 1 });
      scheduleSaveLocalView(15, { x: 6, y: 0, zoom: 1 });
      expect(loadLocalView(14)).toEqual({ x: 5, y: 0, zoom: 1 });
      vi.advanceTimersByTime(LOCAL_VIEW_SAVE_DEBOUNCE_MS);
      expect(loadLocalView(15)).toEqual({ x: 6, y: 0, zoom: 1 });
    });
  });
});
