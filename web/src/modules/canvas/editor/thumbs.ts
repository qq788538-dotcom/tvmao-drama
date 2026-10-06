import { useEffect, useReducer } from 'react';
import { api } from '@/shared/api';

// 画布展示层的缩略图换取器。节点 <img> 默认用缩略图渲染，双击 Lightbox、
// 下载、生成 inputs 仍然读节点的原图 URL（content）——原图始终是唯一真源，
// 缩略图只是这里派生出来的展示形态。
//
// 为什么要走后端换取而不是前端拼参数：TOS 的 x-tos-process 缩放参数必须
// 参与预签名，前端往签名 URL 上加参数会直接 403。MinIO 本地环境无图片
// 处理，后端会原样返回原图签名 URL——功能自动降级，前端无需感知。

// 与后端 thumbDefaultWidth 一致。画布图片节点默认渲染宽约 240-360 CSS px；
// 400 刚好覆盖 1x 屏最大节点宽（200 在 2x 屏明显发虚，768 体积×4 清晰度
// 边际收益小），且与 drama 侧规格一致。
const THUMB_WIDTH = 400;

// 同一 tick 内挂载的一批节点（画布快照加载）合并成一个请求。50ms 足够
// 收拢一次 React commit 里的所有 mount，又短到肉眼无感。
const BATCH_DELAY_MS = 50;

// 单个 POST 携带的 URL 上限，须 ≤ 后端 thumbBatchMax(500)。大画布一次
// mount 上千张图，若整批发出会撞后端「too many urls」→ 400 → 整块画布回退
// 原图、缩略图全失效。这里切片，每批各自成功，与画布规模解耦。
// 签名本身很廉价（服务端只拼签名串、不碰图片），多发几批无压力；真正的
// 图片字节下载/缩放由 <img loading="lazy"> 天然按视口延后，不在这条链路上。
const MAX_URLS_PER_REQUEST = 500;

// 原图 URL → 缩略图 URL。失败/外部 URL 也写入（值=原图），避免每次渲染
// 重复打后端。页面级生命周期即可：签名 URL 本身 24h 过期，比页面活得久。
const cache = new Map<string, string>();
const inflight = new Set<string>();
let queue = new Set<string>();
let timer: ReturnType<typeof setTimeout> | null = null;
const listeners = new Set<() => void>();

function eligible(url: string | undefined | null): url is string {
  // data:/blob: 已在本地，无需也无法缩略。
  return !!url && /^https?:\/\//.test(url);
}

function notify() {
  listeners.forEach((fn) => fn());
}

// 换取一批(≤MAX_URLS_PER_REQUEST)URL 的缩略图并回填缓存。单批失败只影响
// 该批，其余批照常成功——避免一张画布因一批出错整体回退。
async function flushChunk(urls: string[]): Promise<void> {
  try {
    const resp = await api.post('/media/thumbs', { urls, maxWidth: THUMB_WIDTH });
    const thumbs: Record<string, unknown> = resp.data?.thumbs ?? {};
    for (const u of urls) {
      const t = thumbs[u];
      cache.set(u, typeof t === 'string' && t ? t : u);
    }
  } catch {
    // 换取失败直接回退原图并缓存，显示不受影响，只是少了缩略优化。
    for (const u of urls) cache.set(u, u);
  } finally {
    urls.forEach((u) => inflight.delete(u));
  }
}

async function flush(): Promise<void> {
  timer = null;
  const urls = [...queue];
  queue = new Set();
  urls.forEach((u) => inflight.add(u));
  const chunks: Promise<void>[] = [];
  for (let i = 0; i < urls.length; i += MAX_URLS_PER_REQUEST) {
    chunks.push(flushChunk(urls.slice(i, i + MAX_URLS_PER_REQUEST)));
  }
  await Promise.all(chunks);
  notify();
}

function requestThumb(url: string): void {
  if (cache.has(url) || inflight.has(url) || queue.has(url)) return;
  queue.add(url);
  if (timer === null) timer = setTimeout(flush, BATCH_DELAY_MS);
}

/**
 * 返回 url 的展示用缩略图 URL。换取完成前返回 undefined——此时调用方不该
 * 渲染 src，否则浏览器会先下载原图、随后又下载缩略图，流量翻倍。失败/
 * 外部图/本地 MinIO 等换不出缩略图的情况回退原始 url，调用方无需分支。
 */
export function useThumbUrl(url: string | undefined | null): string | undefined {
  const [, force] = useReducer((x: number) => x + 1, 0);
  useEffect(() => {
    if (!eligible(url) || cache.has(url)) return;
    requestThumb(url);
    listeners.add(force);
    return () => {
      listeners.delete(force);
    };
  }, [url]);
  if (!eligible(url)) return url ?? undefined;
  return cache.get(url);
}

/**
 * 多 URL 版 useThumbUrl，给 dangerouslySetInnerHTML 场景（agent 消息里
 * markdown 内联图）用——那里没法为每张图挂一个 ThumbImg 组件。返回
 * 原图→缩略图 映射；未换取到的条目值为 undefined，调用方先别渲染 src。
 */
export function useThumbUrls(urls: string[]): Record<string, string | undefined> {
  const [, force] = useReducer((x: number) => x + 1, 0);
  // urls 每次 render 都是新数组，用内容串做 effect 依赖避免死循环。
  const key = urls.join('\n');
  useEffect(() => {
    const missing = urls.filter((u) => eligible(u) && !cache.has(u));
    if (missing.length === 0) return;
    missing.forEach(requestThumb);
    listeners.add(force);
    return () => {
      listeners.delete(force);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  const out: Record<string, string | undefined> = {};
  for (const u of urls) {
    out[u] = eligible(u) ? cache.get(u) : u;
  }
  return out;
}

// 仅测试用：清空模块级状态，避免用例间互相污染。
export function __resetThumbCacheForTest(): void {
  cache.clear();
  inflight.clear();
  queue = new Set();
  if (timer !== null) {
    clearTimeout(timer);
    timer = null;
  }
  listeners.clear();
}
