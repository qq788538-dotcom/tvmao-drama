import type { CanvasNode } from '../types';

// 进服务端 payload / 复制副本之前的节点净化（P8）。
//
// 抽帧把原分辨率 JPEG dataURL 存在 video-input 的 settings.frames 里：帧是本机
// 的展示缓存（真身在 IndexedDB，见 shared/frameCache），不是画布数据。它留在
// store 里——agent manifest 把它暴露成 machine 字段，UI 也直接读——但不能跟着
// upsert/patch 上服务端，也不能跟着复制粘贴进副本。

/** video-input 节点里只属于本机的抽帧字段。 */
export const VIDEO_INPUT_LOCAL_SETTINGS = ['frames', 'selectedKeyframes', 'extractingFrames'] as const;

/** 超过这个长度的 `data:` 字符串一律不进服务端 payload。小图标/占位图之类几 KB
 *  的 dataURL 照常放行；几十 KB 以上的基本都是误把媒体本体塞进了节点。 */
export const MAX_INLINE_DATA_URL_LENGTH = 64 * 1024;

function isOversizedDataUrl(value: unknown): boolean {
  return typeof value === 'string'
    && value.length > MAX_INLINE_DATA_URL_LENGTH
    && value.startsWith('data:');
}

/** 去掉 video-input 的本机抽帧字段（就地改 settings，调用方传入的必须是自己的拷贝）。 */
export function stripVideoInputLocalSettings(
  type: string | undefined,
  settings: Record<string, unknown> | undefined,
): void {
  if (type !== 'video-input' || !settings) return;
  for (const key of VIDEO_INPUT_LOCAL_SETTINGS) delete settings[key];
}

/**
 * 深拷贝任意 JSON 值，同时丢掉超阈值的 `data:` 字符串：对象里直接去掉该键，
 * 数组里落成 null（JSON.stringify replacer 的固有语义，保持下标不错位）。
 */
export function cloneWithoutOversizedDataUrls<T>(value: T): T {
  return JSON.parse(
    JSON.stringify(value, (_key, v: unknown) => (isOversizedDataUrl(v) ? undefined : v)),
  ) as T;
}

/** 递归检查是否夹带超阈值的 `data:` 字符串。 */
export function hasOversizedDataUrl(value: unknown): boolean {
  if (isOversizedDataUrl(value)) return true;
  if (Array.isArray(value)) return value.some(hasOversizedDataUrl);
  if (value !== null && typeof value === 'object') return Object.values(value).some(hasOversizedDataUrl);
  return false;
}

/**
 * 节点发往服务端前的形态：深拷贝 + 去本机抽帧字段 + 拒绝超大 dataURL。
 * upsert 发的就是它，基线 diff 也拿它比——基线里若还留着存量快照带来的帧，
 * diff 会算出 `null` 删键，顺手把服务端那份清掉。
 */
export function nodeForServer(node: CanvasNode): CanvasNode {
  // 先浅拷一层 settings 把帧摘掉再序列化：几 MB 的帧不必每次落库都 stringify 一遍。
  let source = node;
  if (node.type === 'video-input' && node.settings) {
    const settings = { ...node.settings };
    stripVideoInputLocalSettings(node.type, settings);
    source = { ...node, settings };
  }
  return cloneWithoutOversizedDataUrls(source);
}
