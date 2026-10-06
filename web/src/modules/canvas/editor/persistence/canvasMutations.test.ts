import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CanvasOwnerLostError } from '@/shared/api';
import {
  configureCanvasPersistence,
  emitOwnerLostForTest,
  enqueueCanvasMutation,
  flushCanvasMutations,
  flushNodePatch,
  geometryPatchFromNode,
  getCanvasOwnerToken,
  onCanvasOwnerLost,
  resetCanvasPersistence,
  resetCanvasPersistenceForTest,
  retireCanvasPersistence,
  scheduleNodePatch,
  setCanvasOwnerToken,
  waitForCanvasMutationsIdle,
} from './canvasMutations';

describe('canvasMutations', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    resetCanvasPersistenceForTest();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('runs queued mutations in order', async () => {
    setCanvasOwnerToken('owner-token');
    const order: string[] = [];
    let releaseFirst!: () => void;

    const first = enqueueCanvasMutation(async () => {
      order.push('first:start');
      await new Promise<void>((resolve) => {
        releaseFirst = resolve;
      });
      order.push('first:end');
    });
    const second = enqueueCanvasMutation(async () => {
      order.push('second');
    });

    await Promise.resolve();
    expect(order).toEqual(['first:start']);

    releaseFirst();
    await Promise.all([first, second]);

    expect(order).toEqual(['first:start', 'first:end', 'second']);
  });

  it('debounces node patches for 1500ms and sends the latest merged patch', async () => {
    const patchNode = vi.fn(async () => ({ version: 7 }));
    configureCanvasPersistence({ projectID: 42, patchNode });
    setCanvasOwnerToken('owner-token');

    scheduleNodePatch('n1', { x: 10, settings: { prompt: 'old' } });
    scheduleNodePatch('n1', { y: 20, settings: { prompt: 'new' } });

    await vi.advanceTimersByTimeAsync(1499);
    expect(patchNode).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(1);
    await flushCanvasMutations();

    expect(patchNode).toHaveBeenCalledTimes(1);
    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'n1', {
      x: 10,
      y: 20,
      settings: { prompt: 'new' },
    });
  });

  it('deep-merges nested plain-object fields for same-node patches', async () => {
    const patchNode = vi.fn(async () => ({ version: 7 }));
    configureCanvasPersistence({ projectID: 42, patchNode });
    setCanvasOwnerToken('owner-token');

    scheduleNodePatch('n1', { settings: { prompt: 'a', nested: { seed: 1 } } });
    scheduleNodePatch('n1', { settings: { ratio: '1:1', nested: { quality: 'high' } } });

    await vi.advanceTimersByTimeAsync(1500);
    await flushCanvasMutations();

    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'n1', {
      settings: {
        prompt: 'a',
        ratio: '1:1',
        nested: {
          seed: 1,
          quality: 'high',
        },
      },
    });
  });

  it('replaces arrays and non-plain values while deep-merging node patches', async () => {
    const patchNode = vi.fn(async () => ({ version: 7 }));
    const createdAt = new Date('2026-07-24T00:00:00Z');
    configureCanvasPersistence({ projectID: 42, patchNode });
    setCanvasOwnerToken('owner-token');

    scheduleNodePatch('n1', {
      settings: {
        refs: ['old'],
        meta: { seed: 1 },
        mode: { name: 'draft' },
      },
    });
    scheduleNodePatch('n1', {
      settings: {
        refs: ['new'],
        meta: createdAt,
        mode: 'final',
      },
    });

    await vi.advanceTimersByTimeAsync(1500);
    await flushCanvasMutations();

    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'n1', {
      settings: {
        refs: ['new'],
        meta: createdAt,
        mode: 'final',
      },
    });
  });

  it('routes scheduled flush non-owner errors to onError without unhandled rejection', async () => {
    const err = new Error('patch failed');
    const onError = vi.fn();
    const patchNode = vi.fn(async () => {
      throw err;
    });
    configureCanvasPersistence({ projectID: 42, patchNode, onError });
    setCanvasOwnerToken('owner-token');

    scheduleNodePatch('n1', { x: 12 });
    await vi.advanceTimersByTimeAsync(1500);
    await flushCanvasMutations();

    expect(onError).toHaveBeenCalledWith(err);
  });

  it('drains already-enqueued patch flushes before flushCanvasMutations rejects', async () => {
    const err = new Error('first failed');
    const calls: string[] = [];
    let releaseSecond!: () => void;
    const patchNode = vi.fn(async (_projectID: number, _token: string, nodeID: string) => {
      calls.push(nodeID);
      if (nodeID === 'n1') throw err;
      await new Promise<void>((resolve) => {
        releaseSecond = resolve;
      });
      return { version: 2 };
    });
    configureCanvasPersistence({ projectID: 42, patchNode });
    setCanvasOwnerToken('owner-token');

    scheduleNodePatch('n1', { x: 1 });
    scheduleNodePatch('n2', { x: 2 });

    let settled = false;
    const flushPromise = flushCanvasMutations().catch((flushErr: unknown) => {
      settled = true;
      throw flushErr;
    });
    for (let i = 0; i < 6; i += 1) {
      await Promise.resolve();
    }

    expect(calls).toEqual(['n1', 'n2']);
    expect(settled).toBe(false);

    releaseSecond();
    await expect(flushPromise).rejects.toBe(err);
  });

  it('drops pending node patches when project configuration changes', async () => {
    const patchNode = vi.fn(async () => ({ version: 1 }));
    configureCanvasPersistence({ projectID: 14, patchNode });
    setCanvasOwnerToken('token-14');

    scheduleNodePatch('n1', { x: 1 });
    configureCanvasPersistence({ projectID: 15, patchNode });
    setCanvasOwnerToken('token-15');
    await vi.advanceTimersByTimeAsync(1500);
    await flushCanvasMutations();

    expect(patchNode).not.toHaveBeenCalled();
  });

  it('drops pending node patches when owner token changes before debounce fires', async () => {
    const patchNode = vi.fn(async () => ({ version: 1 }));
    configureCanvasPersistence({ projectID: 14, patchNode });
    setCanvasOwnerToken('token-a');

    scheduleNodePatch('n1', { x: 1 });
    setCanvasOwnerToken('token-b');
    await vi.advanceTimersByTimeAsync(1500);
    await flushCanvasMutations();

    expect(patchNode).not.toHaveBeenCalled();
  });

  it('does not call a new onVersion when an in-flight mutation resolves after reset', async () => {
    let resolveWork!: () => void;
    const oldOnVersion = vi.fn();
    const newOnVersion = vi.fn();
    configureCanvasPersistence({ projectID: 14, onVersion: oldOnVersion });
    setCanvasOwnerToken('token-a');

    const mutation = enqueueCanvasMutation(async () => {
      await new Promise<void>((resolve) => {
        resolveWork = resolve;
      });
      return { version: 7 };
    });
    await Promise.resolve();

    resetCanvasPersistence();
    configureCanvasPersistence({ projectID: 15, onVersion: newOnVersion });
    setCanvasOwnerToken('token-b');
    resolveWork();
    await mutation;

    expect(oldOnVersion).not.toHaveBeenCalled();
    expect(newOnVersion).not.toHaveBeenCalled();
  });

  it('does not call a new onError when a scheduled patch rejects after reset', async () => {
    let rejectPatch!: (err: Error) => void;
    const err = new Error('late failure');
    const oldOnError = vi.fn();
    const newOnError = vi.fn();
    const patchNode = vi.fn(() => new Promise<never>((_resolve, reject) => {
      rejectPatch = reject;
    }));
    configureCanvasPersistence({ projectID: 14, patchNode, onError: oldOnError });
    setCanvasOwnerToken('token-a');

    scheduleNodePatch('n1', { x: 1 });
    await vi.advanceTimersByTimeAsync(1500);
    expect(patchNode).toHaveBeenCalledTimes(1);

    resetCanvasPersistence();
    configureCanvasPersistence({ projectID: 15, onError: newOnError });
    setCanvasOwnerToken('token-b');
    rejectPatch(err);
    for (let i = 0; i < 4; i += 1) {
      await Promise.resolve();
    }

    expect(oldOnError).not.toHaveBeenCalled();
    expect(newOnError).not.toHaveBeenCalled();
  });

  // final-review I-1：sidecar 自动重启 → SidecarGate 卸掉整棵树 → 画布页卸载。
  describe('页面卸载（sidecar 重启）时不丢修改', () => {
    it('retire 会把 debounce 中的 patch 立即入队，reset 之后照常发出', async () => {
      const patchNode = vi.fn(async () => ({ version: 2 }));
      const onVersion = vi.fn();
      configureCanvasPersistence({ projectID: 42, patchNode, onVersion });
      setCanvasOwnerToken('owner-token');
      scheduleNodePatch('n1', { settings: { prompt: '刚打的字' } });

      retireCanvasPersistence();
      expect(getCanvasOwnerToken()).toBe('');
      await vi.advanceTimersByTimeAsync(1500);
      await waitForCanvasMutationsIdle();

      expect(patchNode).toHaveBeenCalledTimes(1);
      expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'n1', { settings: { prompt: '刚打的字' } });
      // 旧会话的回调不再触发
      expect(onVersion).not.toHaveBeenCalled();
    });

    it('reset 不作废已入队、还在排队的 mutation（拦截器会等到新端口再发）', async () => {
      setCanvasOwnerToken('owner-token');
      const order: string[] = [];
      let releaseFirst!: () => void;
      void enqueueCanvasMutation(async () => {
        order.push('first');
        await new Promise<void>((resolve) => { releaseFirst = resolve; });
      });
      void enqueueCanvasMutation(async () => { order.push('second'); });
      await Promise.resolve();

      resetCanvasPersistence();
      // 新会话的 mutation 排在旧任务后面
      setCanvasOwnerToken('owner-token');
      void enqueueCanvasMutation(async () => { order.push('third'); });
      releaseFirst();
      await waitForCanvasMutationsIdle();

      expect(order).toEqual(['first', 'second', 'third']);
    });

    it('waitForCanvasMutationsIdle 等到队列排空才 resolve', async () => {
      setCanvasOwnerToken('owner-token');
      let release!: () => void;
      void enqueueCanvasMutation(() => new Promise<void>((resolve) => { release = resolve; }));
      let idle = false;
      void waitForCanvasMutationsIdle().then(() => { idle = true; });
      await Promise.resolve();
      await Promise.resolve();
      expect(idle).toBe(false);

      release();
      await vi.waitFor(() => expect(idle).toBe(true));
    });

    it('waitForCanvasMutationsIdle 超时放行，不让页面卡在加载', async () => {
      setCanvasOwnerToken('owner-token');
      void enqueueCanvasMutation(() => new Promise<void>(() => {}));
      let idle = false;
      void waitForCanvasMutationsIdle(1000).then(() => { idle = true; });
      await vi.advanceTimersByTimeAsync(999);
      expect(idle).toBe(false);
      await vi.advanceTimersByTimeAsync(1);
      expect(idle).toBe(true);
    });
  });

  it('subscribes and unsubscribes owner-lost listeners', () => {
    const first = vi.fn();
    const second = vi.fn();
    const unsubscribeFirst = onCanvasOwnerLost(first);
    onCanvasOwnerLost(second);
    const err = new CanvasOwnerLostError('lost', 'CANVAS_OWNER_LOST');

    emitOwnerLostForTest(err);
    unsubscribeFirst();
    emitOwnerLostForTest(err);

    expect(first).toHaveBeenCalledTimes(1);
    expect(second).toHaveBeenCalledTimes(2);
  });

  it('isolates owner-lost listener failures', async () => {
    const err = new CanvasOwnerLostError('lost', 'CANVAS_OWNER_LOST');
    const first = vi.fn(() => {
      throw new Error('listener failed');
    });
    const second = vi.fn();
    setCanvasOwnerToken('owner-token');
    onCanvasOwnerLost(first);
    onCanvasOwnerLost(second);

    await expect(enqueueCanvasMutation(async () => {
      throw err;
    })).resolves.toBeUndefined();

    expect(first).toHaveBeenCalledWith(err);
    expect(second).toHaveBeenCalledWith(err);
  });

  it('sets and gets owner token', () => {
    expect(getCanvasOwnerToken()).toBe('');

    setCanvasOwnerToken('owner-token');

    expect(getCanvasOwnerToken()).toBe('owner-token');
  });

  it('resetCanvasPersistence clears owner token and pending patches', async () => {
    const patchNode = vi.fn(async () => ({ version: 1 }));
    const listener = vi.fn();
    configureCanvasPersistence({ projectID: 42, patchNode });
    setCanvasOwnerToken('owner-token');
    onCanvasOwnerLost(listener);
    scheduleNodePatch('n1', { x: 1 });

    resetCanvasPersistence();
    await vi.advanceTimersByTimeAsync(1500);
    await flushCanvasMutations();
    emitOwnerLostForTest(new CanvasOwnerLostError('lost', 'CANVAS_OWNER_LOST'));

    expect(getCanvasOwnerToken()).toBe('');
    expect(patchNode).not.toHaveBeenCalled();
    expect(listener).not.toHaveBeenCalled();
  });

  // 时序守卫。今天不可达（所有 /canvas/:id 入口都会先卸载本页 → reset →
  // projectID 回 null），但一旦出现同组件切项目，configureCanvasPersistence 的兜底
  // reset 会把刚为**新**项目播下的基线和刚设的 ownerToken 一起清掉：字段级 LWW
  // 静默退化成整节点 upsert、结构性 op 被静默丢弃。这条钉子保证它至少喊一声。
  it('切项目时若已有基线/token，兜底 reset 前必须告警（防将来静默退化）', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    configureCanvasPersistence({ projectID: 42 });
    setCanvasOwnerToken('owner-token');

    configureCanvasPersistence({ projectID: 43 });

    expect(warn).toHaveBeenCalledTimes(1);
    expect(String(warn.mock.calls[0][0])).toContain('resetCanvasPersistence');
    // 首次配置（projectID 从 null 起步）不该告警——那是正常路径。
    warn.mockClear();
    resetCanvasPersistenceForTest();
    configureCanvasPersistence({ projectID: 44 });
    expect(warn).not.toHaveBeenCalled();
    warn.mockRestore();
  });

  it('flushNodePatch sends immediately before the debounce timer fires', async () => {
    const patchNode = vi.fn(async () => ({ version: 3 }));
    configureCanvasPersistence({ projectID: 42, patchNode });
    setCanvasOwnerToken('owner-token');

    scheduleNodePatch('n1', { x: 12 });
    await flushNodePatch('n1');
    await vi.advanceTimersByTimeAsync(1500);
    await flushCanvasMutations();

    expect(patchNode).toHaveBeenCalledTimes(1);
    expect(patchNode).toHaveBeenCalledWith(42, 'owner-token', 'n1', { x: 12 });
  });

  it('no-ops queued work when owner token is missing', async () => {
    const work = vi.fn(async () => ({ version: 9 }));

    await enqueueCanvasMutation(work);

    expect(work).not.toHaveBeenCalled();
  });

  it('calls onVersion on successful mutation result', async () => {
    const onVersion = vi.fn();
    configureCanvasPersistence({ projectID: 42, onVersion });
    setCanvasOwnerToken('owner-token');

    await enqueueCanvasMutation(async () => ({ version: 11 }));

    expect(onVersion).toHaveBeenCalledWith(11);
  });

  it('emits owner-lost errors without rejecting the queue', async () => {
    const listener = vi.fn();
    const err = new CanvasOwnerLostError('lost', 'CANVAS_OWNER_LOST');
    setCanvasOwnerToken('owner-token');
    onCanvasOwnerLost(listener);

    await expect(enqueueCanvasMutation(async () => {
      throw err;
    })).resolves.toBeUndefined();

    expect(listener).toHaveBeenCalledWith(err);
  });

  it('rejects non-owner errors from enqueueCanvasMutation', async () => {
    const err = new Error('boom');
    setCanvasOwnerToken('owner-token');

    await expect(enqueueCanvasMutation(async () => {
      throw err;
    })).rejects.toBe(err);
  });

  it('creates geometry patches from nodes', () => {
    expect(geometryPatchFromNode({
      id: 'n1',
      x: 1,
      y: 2,
      width: 300,
      height: 200,
    })).toEqual({
      id: 'n1',
      x: 1,
      y: 2,
      width: 300,
      height: 200,
    });
  });
});
