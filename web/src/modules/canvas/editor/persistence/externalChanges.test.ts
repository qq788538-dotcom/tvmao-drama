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
});
