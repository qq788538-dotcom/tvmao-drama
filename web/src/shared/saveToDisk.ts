// 统一的“保存到本地”入口。所有下载按钮都走这里，不要再直接用 <a download>。
//
// 为什么：Tauri（wry 0.57）在没有注册 on_download 时，macOS WKWebView 会把 <a download>
// 触发的下载直接取消；媒体地址 http://127.0.0.1:<port>/media/... 和页面也不同源，
// 浏览器会忽略 download 属性。所以在 Tauri 里：
//   - 本地媒体：调 Rust 命令 save_media，由 Rust 弹系统保存对话框，再把媒体根目录里的源文件
//     直接复制过去（不经过前端 blob，大视频也不占内存）；
//   - 前端生成的内容（ZIP、截图、JSON）：save_bytes，字节走 IPC raw body，同样由 Rust 弹对话框。
// 写盘路径只来自用户在对话框里的选择，前端从不传目标路径。
// 不在 Tauri 里（纯浏览器调试）时回退到 <a download> / file-saver。
import { invoke, isTauri } from '@tauri-apps/api/core';
import { saveAs } from 'file-saver';

/** saved：已写盘；cancelled：用户取消了对话框；fallback：非 Tauri 环境，交给浏览器下载。 */
export type SaveResult = 'saved' | 'cancelled' | 'fallback';

/** 与 src-tauri/src/media_save.rs 的 FILE_NAME_HEADER 一致。 */
export const FILE_NAME_HEADER = 'x-file-name';

const LOCAL_MEDIA = /^http:\/\/127\.0\.0\.1:\d{1,5}\/media\/[^?#]+/;

/** sidecar 发出的本地媒体 URL，或库里存的 media://<key>。只有这两种能走“直接复制”。 */
export function isLocalMediaUrl(url: string): boolean {
  return url.startsWith('media://') || LOCAL_MEDIA.test(url);
}

function inTauri(): boolean {
  try {
    return isTauri();
  } catch {
    return false;
  }
}

/** 非 Tauri 回退：隐藏 <a download>。target=_blank 兜底跨域时浏览器退化成新标签打开，不把画布顶掉。 */
export function anchorDownload(url: string, name: string): void {
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.target = '_blank';
  a.rel = 'noreferrer';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

/** data: URL 手动解码：CSP 的 connect-src 不放行 data:，不能用 fetch。 */
export function dataUrlToBytes(dataUrl: string): Uint8Array {
  const comma = dataUrl.indexOf(',');
  if (!dataUrl.startsWith('data:') || comma < 0) throw new Error('无效的 data URL');
  const meta = dataUrl.slice(5, comma);
  const payload = dataUrl.slice(comma + 1);
  if (/;base64$/i.test(meta)) {
    const bin = atob(payload);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  }
  return new TextEncoder().encode(decodeURIComponent(payload));
}

/** 把字节交给 Rust：弹保存对话框并写盘。 */
export async function saveBytesToDisk(bytes: Uint8Array, suggestedName: string): Promise<SaveResult> {
  if (!inTauri()) {
    saveAs(new Blob([bytes as BlobPart]), suggestedName);
    return 'fallback';
  }
  const saved = await invoke<string | null>('save_bytes', bytes, {
    headers: { [FILE_NAME_HEADER]: encodeURIComponent(suggestedName) },
  });
  return saved ? 'saved' : 'cancelled';
}

/** Blob → 字节。jsdom 的 Blob 没有 arrayBuffer()，退回 FileReader。 */
async function blobToBytes(blob: Blob): Promise<Uint8Array> {
  if (typeof blob.arrayBuffer === 'function') return new Uint8Array(await blob.arrayBuffer());
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(new Uint8Array(r.result as ArrayBuffer));
    r.onerror = () => reject(r.error ?? new Error('读取文件失败'));
    r.readAsArrayBuffer(blob);
  });
}

/** 前端生成的 Blob（ZIP、导出 JSON 等）。 */
export async function saveBlobToDisk(blob: Blob, suggestedName: string): Promise<SaveResult> {
  if (!inTauri()) {
    saveAs(blob, suggestedName);
    return 'fallback';
  }
  return saveBytesToDisk(await blobToBytes(blob), suggestedName);
}

/**
 * 保存一个媒体 URL。本地媒体走 Rust 直接复制；data:/blob:/其他 http 先在前端取字节再交给 Rust。
 * 用户取消返回 'cancelled'；失败抛错（错误信息可直接展示）。
 */
export async function saveMediaToDisk(url: string, suggestedName: string): Promise<SaveResult> {
  if (!url) throw new Error('没有可保存的文件');
  if (!inTauri()) {
    anchorDownload(url, suggestedName);
    return 'fallback';
  }
  if (isLocalMediaUrl(url)) {
    const saved = await invoke<string | null>('save_media', { url, suggestedName });
    return saved ? 'saved' : 'cancelled';
  }
  let bytes: Uint8Array;
  if (url.startsWith('data:')) {
    bytes = dataUrlToBytes(url);
  } else {
    let resp: Response;
    try {
      resp = await fetch(url);
    } catch {
      throw new Error('这个文件不在本地，暂时无法保存');
    }
    if (!resp.ok) throw new Error(`读取文件失败（HTTP ${resp.status}）`);
    bytes = new Uint8Array(await resp.arrayBuffer());
  }
  return saveBytesToDisk(bytes, suggestedName);
}

/**
 * 一次保存多个媒体。全部是本地媒体时弹一次“选择文件夹”批量复制（重名自动加序号）；
 * 否则逐个弹保存对话框。返回保存的文件数，用户取消返回 null。
 */
export async function saveManyMediaToDisk(items: Array<{ url: string; name: string }>): Promise<number | null> {
  if (items.length === 0) return 0;
  if (!inTauri()) {
    items.forEach((it) => anchorDownload(it.url, it.name));
    return items.length;
  }
  if (items.length === 1) {
    const r = await saveMediaToDisk(items[0].url, items[0].name);
    return r === 'cancelled' ? null : 1;
  }
  if (items.every((it) => isLocalMediaUrl(it.url))) {
    return invoke<number | null>('save_media_batch', { items });
  }
  let n = 0;
  for (const it of items) {
    const r = await saveMediaToDisk(it.url, it.name);
    if (r === 'cancelled') return n > 0 ? n : null;
    n += 1;
  }
  return n;
}

/** antd message 的子集，便于测试。 */
export interface SaveFeedback {
  success: (content: string) => unknown;
  error: (content: string) => unknown;
}

function errorText(err: unknown): string {
  if (typeof err === 'string') return err;
  if (err instanceof Error) return err.message;
  return String(err);
}

/** 统一的提示：保存成功提示“已保存”，取消不提示，失败报出原因（不再“点了没反应”）。 */
export async function saveWithFeedback(
  task: () => Promise<SaveResult | number | null>,
  feedback: SaveFeedback,
): Promise<void> {
  try {
    const r = await task();
    if (r === 'saved') feedback.success('已保存');
    else if (typeof r === 'number' && r > 0 && inTauri()) feedback.success(`已保存 ${r} 个文件`);
  } catch (err) {
    console.error('save to disk failed', err);
    feedback.error(`保存失败：${errorText(err)}`);
  }
}

/** 按 URL 粗略推断扩展名，给建议文件名用（生成产物的 key 没有扩展名，调用方应优先自己给）。 */
export function extFromUrl(url: string, fallback: string): string {
  const path = url.split(/[?#]/)[0];
  const m = /\.([a-z0-9]{2,5})$/i.exec(path);
  return m ? m[1].toLowerCase() : fallback;
}
