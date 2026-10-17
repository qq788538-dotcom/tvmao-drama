import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api, pollCanvasTaskUntilDone } from './api';

// 轮询容错：一次网络抖动不判死任务(曾导致画布节点被误标「失败」且整节点
// persist 把后端刚写回的成功结果覆盖掉)；只有连续失败才放弃，4xx 是确定性
// 错误(任务不存在/无权)立即放弃。

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

  it('连续失败达 5 次才放弃', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(networkError());

    await expect(pollCanvasTaskUntilDone(1, { intervalMs: 0 })).rejects.toThrow();
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

  it('4xx 确定性错误立即放弃不重试', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(httpError(404));

    await expect(pollCanvasTaskUntilDone(1, { intervalMs: 0 })).rejects.toThrow();
    expect(api.get).toHaveBeenCalledTimes(1);
  });

  it('abort 监听每轮摘掉，不在 signal 上越攒越多；取消时在途请求也拿到 signal', async () => {
    const ctrl = new AbortController();
    const add = vi.spyOn(ctrl.signal, 'addEventListener');
    const remove = vi.spyOn(ctrl.signal, 'removeEventListener');
    const spy = vi.spyOn(api, 'get');
    for (let i = 0; i < 5; i++) spy.mockResolvedValueOnce({ data: { status: 'processing' } });
    spy.mockResolvedValueOnce({ data: { status: 'succeeded' } });

    await pollCanvasTaskUntilDone(1, { intervalMs: 0, signal: ctrl.signal });

    expect(add).toHaveBeenCalledTimes(5);
    expect(remove).toHaveBeenCalledTimes(5);
    expect(spy.mock.calls[0][1]).toEqual({ signal: ctrl.signal });
  });

  it('请求在途时取消：立即以 aborted 结束，不当成网络抖动再重试', async () => {
    const ctrl = new AbortController();
    const spy = vi.spyOn(api, 'get').mockImplementation(
      () =>
        new Promise((_resolve, reject) => {
          ctrl.signal.addEventListener('abort', () => reject(new Error('canceled')));
        }),
    );
    const p = pollCanvasTaskUntilDone(1, { intervalMs: 10_000, signal: ctrl.signal });
    ctrl.abort();
    await expect(p).rejects.toThrow('aborted');
    expect(spy).toHaveBeenCalledTimes(1);
  });
});
