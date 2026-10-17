import type { NodeType } from '../types';

// 「什么文件能进画布」的唯一真源。拖入画布和右键菜单的「上传」都走这里,
// 两处各写一套 if/else 迟早会分叉(一处支持 .heic 另一处不支持这种)。

/** 空 MIME 时的扩展名兜底表。macOS 拖 .mov/.heic 给出的 File.type 常常是空串,
 *  只看 MIME 会把合法素材误判成非法。列表只收我们真正接得住的容器格式。 */
const EXTENSION_TYPES: Record<string, NodeType> = {
  // 图片
  jpg: 'input-image', jpeg: 'input-image', png: 'input-image', gif: 'input-image',
  webp: 'input-image', bmp: 'input-image', heic: 'input-image', heif: 'input-image',
  avif: 'input-image', svg: 'input-image',
  // 音频
  mp3: 'input-audio', wav: 'input-audio', aac: 'input-audio', ogg: 'input-audio',
  flac: 'input-audio', m4a: 'input-audio',
  // 视频
  mp4: 'video-input', mov: 'video-input', webm: 'video-input', mkv: 'video-input',
  avi: 'video-input', m4v: 'video-input',
};

function extensionOf(name: string): string {
  const idx = name.lastIndexOf('.');
  // idx <= 0 同时排掉「无扩展名」和「.gitignore 这种纯点开头」两种情况。
  if (idx <= 0 || idx === name.length - 1) return '';
  return name.slice(idx + 1).toLowerCase();
}

/** 文件 → 对应的输入节点类型；判不出返回 null(调用方据此拒绝)。 */
export function nodeTypeForFile(file: File): NodeType | null {
  const mime = file.type ?? '';
  if (mime.startsWith('image/')) return 'input-image';
  if (mime.startsWith('audio/')) return 'input-audio';
  if (mime.startsWith('video/')) return 'video-input';
  return EXTENSION_TYPES[extensionOf(file.name)] ?? null;
}

export interface AcceptedDrop {
  file: File;
  nodeType: NodeType;
}

export interface RejectedDrop {
  name: string;
  reason: string;
}

export interface DropPartition {
  accepted: AcceptedDrop[];
  rejected: RejectedDrop[];
}

/** 把一批拖入的文件分成「能建节点的」和「要弹框拒绝的」。
 *  不因为批次里混了一个非法文件就惩罚整批——用户拖 3 张图 + 1 个 zip 时,
 *  3 张图照常上传,弹框只说 zip 被拒。 */
export function partitionDroppedFiles(files: File[]): DropPartition {
  const accepted: AcceptedDrop[] = [];
  const rejected: RejectedDrop[] = [];
  for (const file of files) {
    const nodeType = nodeTypeForFile(file);
    if (nodeType) {
      accepted.push({ file, nodeType });
      continue;
    }
    // 文件夹在浏览器里就是「空 MIME + 无扩展名」的 File,天然落到这里,不用特判。
    rejected.push({ name: file.name, reason: file.type || '未知类型' });
  }
  return { accepted, rejected };
}

/** 一行最多放几个,超出换行。拖一组素材是主场景,一行铺太远会跑出视口。 */
const ROW_CAPACITY = 4;
const GAP = 24;

/**
 * 多文件的落点排布:以松手处为左上锚点横向铺开,每行 ROW_CAPACITY 个后换行。
 *
 * 返回的是每个节点的**中心点**——store.addNode 按中心放置节点。步长取各节点
 * 自己的宽度(图片 260 / 音频 300 / 视频 580 差异明显),固定步长会让宽节点重叠。
 *
 * 不用 agent 产出物那套 +30/+25 扑克堆叠:那是为「一次一两张」设计的,一次拖 8
 * 张图会叠成一摞,用户得手动扒开。
 */
export function layoutDropPoints(
  sizes: Array<{ width: number; height: number }>,
  origin: { x: number; y: number },
): Array<{ x: number; y: number }> {
  const points: Array<{ x: number; y: number }> = [];
  let cursorX = origin.x;
  let rowY = origin.y;
  let rowHeight = 0;
  sizes.forEach((size, i) => {
    if (i > 0 && i % ROW_CAPACITY === 0) {
      cursorX = origin.x;
      rowY += rowHeight + GAP;
      rowHeight = 0;
    }
    points.push({ x: cursorX + size.width / 2, y: rowY + size.height / 2 });
    cursorX += size.width + GAP;
    rowHeight = Math.max(rowHeight, size.height);
  });
  return points;
}
