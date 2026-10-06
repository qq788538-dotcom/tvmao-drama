// 故事板的四个分区，以及画布节点往这四个分区的映射。
// 故事板只展示「产物」，所以交互式工作台（director-desk）和纯工具节点
// （image-compare / local-save）不进。script 按用户要求也不进。
import type { CanvasNode } from '../editor/types';

export type StoryboardZone = 'audio' | 'text' | 'image' | 'video';

export const ZONE_LABELS: Record<StoryboardZone, string> = {
  audio: '音频',
  text: '文本',
  image: '图片',
  video: '视频',
};

// subtitle-erase / video-upscale / video-whitemodel 吃视频吐视频，产物就是视频，所以归 video
// 而不是单开一类——故事板是按「这是什么东西」分栏，不是按「怎么来的」。
const ZONE_BY_TYPE: Record<string, StoryboardZone> = {
  'input-audio': 'audio',
  'gen-audio': 'audio',
  'text-node': 'text',
  'input-image': 'image',
  'gen-image': 'image',
  'video-input': 'video',
  'gen-video': 'video',
  'subtitle-erase': 'video',
  'video-upscale': 'video',
  'video-whitemodel': 'video',
};

// preview 没有 kind 时按 URL 扩展名兜底认视频——**这条规则必须与
// PreviewNode 自己的判断逐字一致**（PreviewNode.tsx 里的同款正则）。
// 两边不一致的话，同一个节点会在画布上渲染成视频播放器、在故事板里却
// 被分进图片栏。
const VIDEO_EXT_RE = /\.(mp4|webm|mov)(\?|$)/i;

/**
 * 节点 → 故事板分区。不属于任何分区（含未知类型）返回 null。
 *
 * 吃整个 node 而不是 node.type：preview 是通用的「媒体容器」类型，
 * 归哪一栏取决于 `settings.kind`（image / video / audio，三种都真实存在，
 * 见 agent/canvasSummary.ts 的同款三分），光看类型判断不了。
 *
 * 未知类型静默返回 null 而不是抛错：节点类型清单还在增长，把它当封闭枚举
 * 会让每加一种类型都要回来改故事板，漏改就是运行时崩。
 */
export function zoneOf(node: CanvasNode): StoryboardZone | null {
  if (node.type === 'preview') {
    const kind = node.settings?.kind as string | undefined;
    if (kind === 'audio') return 'audio';
    if (kind === 'video') return 'video';
    if (kind === 'image') return 'image';
    // kind 缺省：跟 PreviewNode 一样按扩展名兜底，认不出就当图片。
    return VIDEO_EXT_RE.test(node.content ?? '') ? 'video' : 'image';
  }
  return ZONE_BY_TYPE[node.type] ?? null;
}

/** 按分区给节点分组。四个分区的 key 恒在（可能是空数组），消费方不用判空。 */
export function groupByZone(nodes: CanvasNode[]): Record<StoryboardZone, CanvasNode[]> {
  const grouped: Record<StoryboardZone, CanvasNode[]> = {
    audio: [],
    text: [],
    image: [],
    video: [],
  };
  for (const n of nodes) {
    const zone = zoneOf(n);
    if (zone) grouped[zone].push(n);
  }
  return grouped;
}
