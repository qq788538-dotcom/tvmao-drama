import { describe, it, expect, vi } from 'vitest';
import { createBatchLoader } from './batchQueries';

describe('createBatchLoader', () => {
  it('同一时间窗内的 load 合并成一次 batchFn，各自拿到自己的值', async () => {
    const batchFn = vi.fn(async (keys: string[]) => new Map(keys.map((k) => [k, k.toUpperCase()])));
    const load = createBatchLoader(batchFn, { keyOf: (k: string) => k });

    const [a, b, c] = await Promise.all([load('a'), load('b'), load('c')]);

    expect([a, b, c]).toEqual(['A', 'B', 'C']);
    expect(batchFn).toHaveBeenCalledTimes(1);
    expect(batchFn.mock.calls[0][0].sort()).toEqual(['a', 'b', 'c']);
  });

  it('同一个 key 多次 load 去重，只进批一次', async () => {
    const batchFn = vi.fn(async (keys: string[]) => new Map(keys.map((k) => [k, k.length])));
    const load = createBatchLoader(batchFn, { keyOf: (k: string) => k });

    const [x1, x2] = await Promise.all([load('same'), load('same')]);

    expect(x1).toBe(4);
    expect(x2).toBe(4);
    expect(batchFn.mock.calls[0][0]).toEqual(['same']);
  });

  it('超过 maxBatch 时切成多批', async () => {
    const batchFn = vi.fn(async (keys: number[]) => new Map(keys.map((k) => [String(k), k])));
    const load = createBatchLoader(batchFn, { keyOf: (k: number) => String(k), maxBatch: 2 });

    const vals = await Promise.all([load(1), load(2), load(3), load(4), load(5)]);

    expect(vals).toEqual([1, 2, 3, 4, 5]);
    // 5 个 key、每批 2 → 3 批。
    expect(batchFn).toHaveBeenCalledTimes(3);
    for (const call of batchFn.mock.calls) {
      expect(call[0].length).toBeLessThanOrEqual(2);
    }
  });

  it('batchFn 没返回某 key → 该调用方拿到 undefined', async () => {
    const batchFn = async () => new Map<string, number>(); // 空 map
    const load = createBatchLoader(batchFn, { keyOf: (k: string) => k });

    await expect(load('missing')).resolves.toBeUndefined();
  });

  it('整批失败 → 该批 waiter 全 reject', async () => {
    const batchFn = async () => {
      throw new Error('boom');
    };
    const load = createBatchLoader(batchFn, { keyOf: (k: string) => k });

    await expect(load('x')).rejects.toThrow('boom');
  });
});
