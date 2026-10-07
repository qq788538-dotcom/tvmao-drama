import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setSidecarSession } from '@/shared/sidecarSession';

const getSnapshot = vi.fn();
vi.mock('@/shared/api', async (orig) => ({ ...(await orig<object>()), getSnapshot: (...a: unknown[]) => getSnapshot(...a) }));

const reload = vi.fn();
const idle = vi.fn(() => true);
vi.mock('../persistence/externalChanges', () => ({
  installPointerTracker: () => () => {},
  isCanvasIdle: () => idle(),
  reloadCanvasFromServer: (...a: unknown[]) => reload(...a),
}));

class FakeES {
  static all: FakeES[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((m: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  constructor(public url: string) { FakeES.all.push(this); }
  close() { this.closed = true; }
  open() { this.onopen?.(); }
  emit(o: unknown) { this.onmessage?.({ data: JSON.stringify(o) }); }
  fail() { this.onerror?.(); }
}

import { useExternalChanges } from './useExternalChanges';

describe('useExternalChanges', () => {
  beforeEach(() => {
    FakeES.all = [];
    vi.stubGlobal('EventSource', FakeES);
    setSidecarSession({ port: 1234, token: 't k' });
    reload.mockReset().mockResolvedValue(4);
    idle.mockReset().mockReturnValue(true);
    getSnapshot.mockReset();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  const mount = (onReloaded = vi.fn()) =>
    renderHook(() => useExternalChanges({ projectId: 14, enabled: true, initialVersion: 3, onReloaded }));

  it('subscribes with token and reloads on newer cli event', async () => {
    const onReloaded = vi.fn();
    mount(onReloaded);
    await waitFor(() => expect(FakeES.all).toHaveLength(1));
    expect(FakeES.all[0].url).toBe('http://127.0.0.1:1234/api/v1/projects/14/events?token=t%20k');
    act(() => FakeES.all[0].open());
    act(() => FakeES.all[0].emit({ projectId: 14, version: 4, source: 'cli' }));
    await waitFor(() => expect(reload).toHaveBeenCalledWith(14));
    await waitFor(() => expect(onReloaded).toHaveBeenCalledWith(4));
    // 同版本或更旧的事件不再触发
    act(() => FakeES.all[0].emit({ projectId: 14, version: 4, source: 'cli' }));
    act(() => FakeES.all[0].emit({ projectId: 14, version: 2, source: 'cli' }));
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('ignores events from other projects or sources', async () => {
    mount();
    await waitFor(() => expect(FakeES.all).toHaveLength(1));
    act(() => FakeES.all[0].emit({ projectId: 99, version: 10, source: 'cli' }));
    act(() => FakeES.all[0].emit({ projectId: 14, version: 10, source: 'agent' }));
    expect(reload).not.toHaveBeenCalled();
  });

  it('waits until canvas is idle before reloading', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    idle.mockReturnValue(false);
    mount();
    await waitFor(() => expect(FakeES.all).toHaveLength(1));
    act(() => FakeES.all[0].emit({ projectId: 14, version: 4, source: 'cli' }));
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(reload).not.toHaveBeenCalled();
    idle.mockReturnValue(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(600); });
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('retries when a reload is discarded by a racing local mutation', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    reload.mockResolvedValueOnce(null).mockResolvedValueOnce(4);
    mount();
    await waitFor(() => expect(FakeES.all).toHaveLength(1));
    act(() => FakeES.all[0].emit({ projectId: 14, version: 4, source: 'cli' }));
    await act(async () => { await vi.advanceTimersByTimeAsync(600); });
    expect(reload).toHaveBeenCalledTimes(2);
  });

  it('reconnects with backoff and compares version once after reconnect', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    getSnapshot.mockResolvedValue({ payload: {}, version: 5, syncedAt: null });
    mount();
    await waitFor(() => expect(FakeES.all).toHaveLength(1));
    act(() => FakeES.all[0].open());
    expect(getSnapshot).not.toHaveBeenCalled(); // 首次连接不比较
    act(() => FakeES.all[0].fail());
    expect(FakeES.all[0].closed).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(1100); });
    expect(FakeES.all).toHaveLength(2);
    act(() => FakeES.all[1].open());
    await waitFor(() => expect(getSnapshot).toHaveBeenCalledWith(14));
    await waitFor(() => expect(reload).toHaveBeenCalledTimes(1));
  });

  it('does not reload after reconnect when the server version only moved by local writes', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    getSnapshot.mockResolvedValue({ payload: {}, version: 5, syncedAt: null });
    const r = mount();
    await waitFor(() => expect(FakeES.all).toHaveLength(1));
    act(() => FakeES.all[0].open());
    // 本地两次写入的回执：3 -> 4 -> 5
    act(() => { r.result.current.noteLocalVersion(4); r.result.current.noteLocalVersion(5); });
    act(() => FakeES.all[0].fail());
    await act(async () => { await vi.advanceTimersByTimeAsync(1100); });
    act(() => FakeES.all[1].open());
    await waitFor(() => expect(getSnapshot).toHaveBeenCalledWith(14));
    await act(async () => { await vi.advanceTimersByTimeAsync(600); });
    expect(reload).not.toHaveBeenCalled();
  });

  it('still reloads for a lower-numbered cli event that arrives after a local receipt', async () => {
    reload.mockResolvedValue(5);
    const r = mount();
    await waitFor(() => expect(FakeES.all).toHaveLength(1));
    act(() => FakeES.all[0].open());
    // CLI 先写成 v4，本地回执 v5 先到，CLI 事件后到：前端还没看到 CLI 的改动
    act(() => r.result.current.noteLocalVersion(5));
    act(() => FakeES.all[0].emit({ projectId: 14, version: 4, source: 'cli' }));
    await waitFor(() => expect(reload).toHaveBeenCalledTimes(1));
  });

  it('reloads after reconnect when a non-contiguous local receipt reveals a missed write', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    getSnapshot.mockResolvedValue({ payload: {}, version: 5, syncedAt: null });
    reload.mockResolvedValue(5);
    const r = mount();
    await waitFor(() => expect(FakeES.all).toHaveLength(1));
    act(() => FakeES.all[0].open());
    act(() => FakeES.all[0].fail());
    // 断线期间 CLI 写成 v4（事件丢了），本地写入回执 v5：中间跳号
    act(() => r.result.current.noteLocalVersion(5));
    await act(async () => { await vi.advanceTimersByTimeAsync(1100); });
    act(() => FakeES.all[1].open());
    await waitFor(() => expect(getSnapshot).toHaveBeenCalledWith(14));
    await waitFor(() => expect(reload).toHaveBeenCalledTimes(1));
  });

  it('closes the stream on unmount', async () => {
    const r = mount();
    await waitFor(() => expect(FakeES.all).toHaveLength(1));
    r.unmount();
    expect(FakeES.all[0].closed).toBe(true);
  });
});
