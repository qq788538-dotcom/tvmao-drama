import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  api,
  CanvasPollStalledError,
  CanvasTaskGoneError,
  isCanvasPollStalled,
  pollCanvasTaskUntilDone,
} from './api';

// 轮询容错：一次网络抖动不判死任务(曾导致画布节点被误标「失败」且整节点
// persist 把后端刚写回的成功结果覆盖掉)。连续够不着后端以「挂起」结束、交给
// 对账(P23)；只有连续多次 404 才以「任务不存在」结束。

const networkError = () => Object.assign(new Error('Network Error'), {});
const httpError = (status: number) =>
  Object.assign(new Error(`http ${status}`), { response: { status } });

describe('pollCanvasTaskUntilDone 容错', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('单次网络错误不判死,恢复后继续轮询到终态', async () => {
    vi.spyOn(api, 'get')
      .mockRejectedValueOnce(networkError())
      .mockResolvedValueOnce({ data: { status: 'processing' } })
      .mockResolvedValueOnce({ data: { status: 'succeeded', outputUrls: ['u'] } });

    const st = await pollCanvasTaskUntilDone(1, { intervalMs: 0 });

    expect(st.status).toBe('succeeded');
    expect(api.get).toHaveBeenCalledTimes(3);
  });

  it('连续失败达 5 次以「挂起」结束,不是普通错误', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(networkError());

    const err = await pollCanvasTaskUntilDone(1, { intervalMs: 0 }).catch((e) => e);
    expect(err).toBeInstanceOf(CanvasPollStalledError);
    expect(isCanvasPollStalled(err)).toBe(true);
    expect(api.get).toHaveBeenCalledTimes(5);
  });

  it('成功响应重置连续失败计数', async () => {
    const spy = vi.spyOn(api, 'get');
    // 4 次失败 → 一次 processing(计数归零) → 再 4 次失败 → 成功。
    // 若计数不归零,第 5 次失败就会提前放弃,走不到最后的 succeeded。
    for (let i = 0; i < 4; i++) spy.mockRejectedValueOnce(networkError());
    spy.mockResolvedValueOnce({ data: { status: 'processing' } });
    for (let i = 0; i < 4; i++) spy.mockRejectedValueOnce(networkError());
    spy.mockResolvedValueOnce({ data: { status: 'succeeded' } });

    const st = await pollCanvasTaskUntilDone(1, { intervalMs: 0 });

    expect(st.status).toBe('succeeded');
  });

  // 原先任意 4xx 立即放弃,单次 404 就让节点判死；现在连续 3 次 404 才算任务不存在。
  it('连续 3 次 404 才以「任务不存在」结束', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(httpError(404));

    const err = await pollCanvasTaskUntilDone(1, { intervalMs: 0 }).catch((e) => e);
    expect(err).toBeInstanceOf(CanvasTaskGoneError);
    expect(isCanvasPollStalled(err)).toBe(false);
    expect(api.get).toHaveBeenCalledTimes(3);
  });

  it('404 中间夹一次正常响应就重新计数', async () => {
    const spy = vi.spyOn(api, 'get');
    spy.mockRejectedValueOnce(httpError(404)).mockRejectedValueOnce(httpError(404));
    spy.mockResolvedValueOnce({ data: { status: 'processing' } });
    spy.mockRejectedValueOnce(httpError(404)).mockRejectedValueOnce(httpError(404));
    spy.mockResolvedValueOnce({ data: { status: 'succeeded' } });

    const st = await pollCanvasTaskUntilDone(1, { intervalMs: 0 });
    expect(st.status).toBe('succeeded');
  });

  it('非 404 的 4xx(如 sidecar 换了 token 的 401)按够不着处理,挂起而不判死', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(httpError(401));

    const err = await pollCanvasTaskUntilDone(1, { intervalMs: 0 }).catch((e) => e);
    expect(err).toBeInstanceOf(CanvasPollStalledError);
    expect(api.get).toHaveBeenCalledTimes(5);
  });

  it('abort 监听每轮摘掉，不在 signal 上越攒越多；取消时在途请求也拿到 signal', async () => {
    const ctrl = new AbortController();
    const add = vi.spyOn(AbortSignal.prototype, 'addEventListener');
    const remove = vi.spyOn(AbortSignal.prototype, 'removeEventListener');
    const spy = vi.spyOn(api, 'get');
    for (let i = 0; i < 5; i++) spy.mockResolvedValueOnce({ data: { status: 'processing' } });
    spy.mockResolvedValueOnce({ data: { status: 'succeeded' } });

    await pollCanvasTaskUntilDone(1, { intervalMs: 0, signal: ctrl.signal });

    // 调用方的 signal 只挂一个退订监听,结束时摘掉；内部那一路的 signal 每轮挂一个、到点摘一个。
    const onCaller = (m: typeof add | typeof remove) =>
      m.mock.contexts.filter((c) => c === ctrl.signal).length;
    expect(onCaller(add)).toBe(1);
    expect(onCaller(remove)).toBe(1);
    expect(add.mock.calls.length - onCaller(add)).toBe(5);
    expect(remove.mock.calls.length - onCaller(remove)).toBe(5);
    const reqSignal = (spy.mock.calls[0][1] as { signal: AbortSignal }).signal;
    expect(reqSignal).toBeInstanceOf(AbortSignal);
  });

  it('请求在途时取消：立即以 aborted 结束，不当成网络抖动再重试', async () => {
    const ctrl = new AbortController();
    let reqSignal: AbortSignal | undefined;
    const spy = vi.spyOn(api, 'get').mockImplementation(
      (_url, cfg) =>
        new Promise((_resolve, reject) => {
          reqSignal = (cfg as { signal: AbortSignal }).signal;
          reqSignal.addEventListener('abort', () => reject(new Error('canceled')));
        }),
    );
    const p = pollCanvasTaskUntilDone(1, { intervalMs: 10_000, signal: ctrl.signal });
    ctrl.abort();
    await expect(p).rejects.toThrow('aborted');
    // 唯一的订阅者退订了:在途请求一并取消。
    expect(reqSignal?.aborted).toBe(true);
    expect(spy).toHaveBeenCalledTimes(1);
  });
});

// P31:故事板详情把同一节点再挂一份,两份组件各调一次轮询——按 taskId 只跑一路。
describe('pollCanvasTaskUntilDone 按 taskId 去重', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('同一 taskId 的两个订阅者共用一路请求、拿到同一个结果', async () => {
    const spy = vi.spyOn(api, 'get');
    spy.mockResolvedValueOnce({ data: { status: 'processing' } });
    spy.mockResolvedValueOnce({ data: { status: 'succeeded', outputUrls: ['u'] } });

    const a = pollCanvasTaskUntilDone(7, { intervalMs: 0 });
    const b = pollCanvasTaskUntilDone(7, { intervalMs: 0 });
    const [ra, rb] = await Promise.all([a, b]);

    expect(ra).toBe(rb);
    expect(ra.status).toBe('succeeded');
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it('不同 taskId 各跑各的', async () => {
    const spy = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
      data: { status: 'succeeded', outputUrls: [String(url)] },
    }));

    const [a, b] = await Promise.all([
      pollCanvasTaskUntilDone(8, { intervalMs: 0 }),
      pollCanvasTaskUntilDone(9, { intervalMs: 0 }),
    ]);

    expect(a.outputUrls).toEqual(['/canvas-tasks/8']);
    expect(b.outputUrls).toEqual(['/canvas-tasks/9']);
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it('一个订阅者退订不影响另一个；全部退订才停掉那一路', async () => {
    vi.useFakeTimers();
    const spy = vi.spyOn(api, 'get').mockResolvedValue({ data: { status: 'processing' } });
    const c1 = new AbortController();
    const c2 = new AbortController();
    const p1 = pollCanvasTaskUntilDone(10, { intervalMs: 1000, signal: c1.signal });
    const p2 = pollCanvasTaskUntilDone(10, { intervalMs: 1000, signal: c2.signal });

    await vi.advanceTimersByTimeAsync(0);
    expect(spy).toHaveBeenCalledTimes(1);

    c1.abort();
    await expect(p1).rejects.toThrow('aborted');
    await vi.advanceTimersByTimeAsync(1000);
    // 另一个订阅者还在,那一路照常往下轮询。
    expect(spy).toHaveBeenCalledTimes(2);

    c2.abort();
    await expect(p2).rejects.toThrow('aborted');
    await vi.advanceTimersByTimeAsync(5000);
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it('全部退订后再订阅同一 taskId:重新起一路,不复用已停掉的那路', async () => {
    const spy = vi.spyOn(api, 'get');
    const c1 = new AbortController();
    spy.mockImplementationOnce(
      (_url, cfg) =>
        new Promise((_resolve, reject) => {
          (cfg as { signal: AbortSignal }).signal.addEventListener('abort', () =>
            reject(new Error('canceled')),
          );
        }),
    );
    const p1 = pollCanvasTaskUntilDone(11, { intervalMs: 0, signal: c1.signal });
    c1.abort();
    await expect(p1).rejects.toThrow('aborted');

    spy.mockResolvedValueOnce({ data: { status: 'succeeded' } });
    const st = await pollCanvasTaskUntilDone(11, { intervalMs: 0 });
    expect(st.status).toBe('succeeded');
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it('一路结束后(挂起)再订阅会重新开始轮询', async () => {
    const spy = vi.spyOn(api, 'get');
    for (let i = 0; i < 5; i++) spy.mockRejectedValueOnce(networkError());
    await expect(pollCanvasTaskUntilDone(12, { intervalMs: 0 })).rejects.toBeInstanceOf(
      CanvasPollStalledError,
    );

    spy.mockResolvedValueOnce({ data: { status: 'succeeded' } });
    const st = await pollCanvasTaskUntilDone(12, { intervalMs: 0 });
    expect(st.status).toBe('succeeded');
    expect(spy).toHaveBeenCalledTimes(6);
  });
});
