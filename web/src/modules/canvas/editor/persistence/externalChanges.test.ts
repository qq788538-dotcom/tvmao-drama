import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useCanvasStore } from '../store';

const getSnapshot = vi.fn();
vi.mock('@/shared/api', async (orig) => ({ ...(await orig<object>()), getSnapshot: (...a: unknown[]) => getSnapshot(...a) }));

const idle = vi.fn(() => true);
const seq = vi.fn(() => 0);
const seed = vi.fn();
vi.mock('./canvasMutations', async (orig) => ({
  ...(await orig<object>()),
  isCanvasPersistenceIdle: () => idle(),
  canvasMutationSeq: () => seq(),
  seedCanvasBaselines: (...a: unknown[]) => seed(...a),
}));

import { installPointerTracker, isCanvasIdle, reloadCanvasFromServer } from './externalChanges';

describe('isCanvasIdle', () => {
  beforeEach(() => {
    idle.mockReturnValue(true);
    useCanvasStore.setState({ connecting: null });
    (document.activeElement as HTMLElement | null)?.blur();
  });

  it('defers while pointer is down', () => {
    const off = installPointerTracker(window);
    window.dispatchEvent(new Event('pointerdown'));
    expect(isCanvasIdle()).toBe(false);
    window.dispatchEvent(new Event('pointerup'));
    expect(isCanvasIdle()).toBe(true);
    off();
  });

  it('not idle while a text input is focused', () => {
    const ta = document.createElement('textarea');
    document.body.appendChild(ta);
    ta.focus();
    expect(isCanvasIdle()).toBe(false);
    ta.remove();
  });

  it('idle when the window lost focus even if a text input still holds activeElement', () => {
    const ta = document.createElement('textarea');
    document.body.appendChild(ta);
    ta.focus();
    const spy = vi.spyOn(document, 'hasFocus').mockReturnValue(false);
    expect(isCanvasIdle()).toBe(true);
    spy.mockReturnValue(true);
    expect(isCanvasIdle()).toBe(false);
    spy.mockRestore();
    ta.remove();
  });

  it('not idle while local mutations are in flight', () => {
    idle.mockReturnValue(false);
    expect(isCanvasIdle()).toBe(false);
  });
});

describe('reloadCanvasFromServer', () => {
  beforeEach(() => {
    getSnapshot.mockReset();
    seed.mockReset();
    seq.mockReturnValue(0);
    idle.mockReturnValue(true);
  });

  it('loads server nodes keeping view and returns version', async () => {
    useCanvasStore.setState({ view: { x: 1, y: 2, zoom: 3 } });
    getSnapshot.mockResolvedValue({ payload: { nodes: [{ id: 'c' }], connections: [] }, version: 9, syncedAt: null });
    await expect(reloadCanvasFromServer(14)).resolves.toBe(9);
    expect(getSnapshot).toHaveBeenCalledWith(14);
    expect(useCanvasStore.getState().nodes.map((n) => n.id)).toEqual(['c']);
    expect(useCanvasStore.getState().view).toEqual({ x: 1, y: 2, zoom: 3 });
    expect(seed).toHaveBeenCalledWith([{ id: 'c' }]);
  });

  it('discards a reload that raced a local mutation', async () => {
    useCanvasStore.setState({ nodes: [{ id: 'a' } as never] });
    seq.mockReturnValueOnce(0).mockReturnValueOnce(1);
    getSnapshot.mockResolvedValue({ payload: { nodes: [], connections: [] }, version: 9, syncedAt: null });
    await expect(reloadCanvasFromServer(14)).resolves.toBeNull();
    expect(useCanvasStore.getState().nodes.map((n) => n.id)).toEqual(['a']);
    expect(seed).not.toHaveBeenCalled();
  });

  it('discards the snapshot when the user started editing while it was fetching', async () => {
    useCanvasStore.setState({ nodes: [{ id: 'a' } as never] });
    const ta = document.createElement('textarea');
    document.body.appendChild(ta);
    const spy = vi.spyOn(document, 'hasFocus').mockReturnValue(true);
    getSnapshot.mockImplementation(async () => {
      ta.focus(); // 拉取期间用户点进了输入框
      return { payload: { nodes: [{ id: 'c' }], connections: [] }, version: 9, syncedAt: null };
    });
    await expect(reloadCanvasFromServer(14)).resolves.toBeNull();
    expect(useCanvasStore.getState().nodes.map((n) => n.id)).toEqual(['a']);
    expect(seed).not.toHaveBeenCalled();
    spy.mockRestore();
    ta.remove();
  });

  it('treats non-array nodes/connections as empty', async () => {
    getSnapshot.mockResolvedValue({ payload: { nodes: { bad: 1 }, connections: 'x' }, version: 9, syncedAt: null });
    await expect(reloadCanvasFromServer(14)).resolves.toBe(9);
    expect(useCanvasStore.getState().nodes).toEqual([]);
    expect(useCanvasStore.getState().connections).toEqual([]);
    expect(seed).toHaveBeenCalledWith([]);
  });
});
