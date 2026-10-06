import { fetchSeedanceStatus, type SeedanceAssetState } from '@/shared/api';

// 画布合批加载器。大画布上每个节点各自的合规状态查询本来是「一个节点一个
// 请求」，几百个节点一进画布就把同一个端点打成几百条请求。这里把同一时间窗内的
// load(key) 调用攒起来、按 key 去重，发一次批量请求，再按 key 把结果分发回各自的
// promise——react-query 的 queryFn 只需从「单发」换成「走 load」，缓存/staleTime/
// 轮询语义全不变，请求数从 N 塌成 ⌈N/maxBatch⌉。与 editor/thumbs.ts 同一套路。

// 同一 React commit 里的所有 mount 落在这个窗口内合并成一批。50ms 足够收拢一次
// 画布快照加载的全部节点，又短到肉眼无感。
const BATCH_WINDOW_MS = 50;

// 单批 key 上限。两个端点后端都无 cap，这里自己约束：太大则请求体胖、DB IN 长；
// 太小则并发请求变多。150 折中——600 节点 → 4 条并发请求，每条都很轻。
const MAX_KEYS_PER_BATCH = 150;

interface Waiter<V> {
  resolve: (v: V | undefined) => void;
  reject: (e: unknown) => void;
}

interface QueuedKey<K, V> {
  key: K;
  waiters: Waiter<V>[];
}

interface BatchLoaderOptions<K> {
  keyOf: (key: K) => string;
  windowMs?: number;
  maxBatch?: number;
}

/**
 * 通用合批器工厂。batchFn 收一组去重后的 key，返回「keyOf(key) → 值」的 Map；
 * 某个 key 不在 Map 里 → 该调用方拿到 undefined（如 seedance「未校验过的 url
 * 不出现」）。整批请求失败则该批所有 waiter reject，react-query 各自置 isError。
 */
export function createBatchLoader<K, V>(
  batchFn: (keys: K[]) => Promise<Map<string, V>>,
  opts: BatchLoaderOptions<K>,
): (key: K) => Promise<V | undefined> {
  const windowMs = opts.windowMs ?? BATCH_WINDOW_MS;
  const maxBatch = opts.maxBatch ?? MAX_KEYS_PER_BATCH;
  let queue = new Map<string, QueuedKey<K, V>>();
  let timer: ReturnType<typeof setTimeout> | null = null;

  async function runChunk(chunk: QueuedKey<K, V>[]): Promise<void> {
    try {
      const result = await batchFn(chunk.map((e) => e.key));
      for (const e of chunk) {
        const v = result.get(opts.keyOf(e.key));
        e.waiters.forEach((w) => w.resolve(v));
      }
    } catch (err) {
      for (const e of chunk) e.waiters.forEach((w) => w.reject(err));
    }
  }

  function flush(): void {
    timer = null;
    const batch = [...queue.values()];
    queue = new Map();
    for (let i = 0; i < batch.length; i += maxBatch) {
      void runChunk(batch.slice(i, i + maxBatch));
    }
  }

  return function load(key: K): Promise<V | undefined> {
    return new Promise<V | undefined>((resolve, reject) => {
      const k = opts.keyOf(key);
      const existing = queue.get(k);
      if (existing) {
        existing.waiters.push({ resolve, reject });
      } else {
        queue.set(k, { key, waiters: [{ resolve, reject }] });
      }
      if (timer === null) timer = setTimeout(flush, windowMs);
    });
  };
}

/**
 * seedance 合规状态合批。各图片节点传自己的 url，落到一次 seedance/status。
 * 未校验过的 url 不在后端返回里 → load 得 undefined → 节点不显示角标。
 */
export const loadSeedanceStatus = createBatchLoader<string, SeedanceAssetState>(
  async (urls) => new Map(Object.entries(await fetchSeedanceStatus(urls))),
  { keyOf: (url) => url },
);
