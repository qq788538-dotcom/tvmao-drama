// 宫格切分的执行侧：加载原图 → 逐格 canvas 裁剪 → 批量建 input-image 节点
// （连回源）→ 逐格上传。纯前端切图，零后端改动、零积分
// （本地裁剪后直传对象存储）。
import { useCanvasStore } from '../../store';
import { uploadFileIntoNode } from '../../utils/uploadIntoNode';
import { mediaCrossOrigin } from '../../utils/mediaCrossOrigin';
import { cellNodePosition, cellNodeSize, cellRects, type CellRect } from './gridSplitMath';

export interface GridSplitResult {
  created: number;
  failed: number;
}

/**
 * selected 的 key 是 `${row}-${col}`（0 基）。返回创建数与上传失败数——
 * 单格上传失败已由 uploadFileIntoNode 落在节点 status/error 上，调用方只
 * 需汇总提示。裁剪阶段的失败（跨域污染 canvas 等）直接 throw：那种失败是
 * 全体格子一起失败，先抛错就不会留下一排空壳节点。
 */
export async function executeGridSplit(opts: {
  sourceId: string;
  url: string;
  rows: number;
  cols: number;
  selected: ReadonlySet<string>;
}): Promise<GridSplitResult> {
  const img = await loadImage(opts.url);
  const rects = cellRects(img.naturalWidth, img.naturalHeight, opts.rows, opts.cols).filter(
    (r) => opts.selected.has(`${r.row}-${r.col}`),
  );
  if (rects.length === 0) return { created: 0, failed: 0 };

  // 先裁全部格子再建节点：裁剪失败几乎只有「canvas 被跨域污染」一种（历史
  // 节点可能落了不带 CORS 头的上游裸 URL），那是所有格子一起失败——先裁
  // 就不会先铺出一排节点再逐个报错。25 格 PNG blob 常驻内存也就几 MB。
  const files: File[] = [];
  for (const rect of rects) {
    files.push(await cropCell(img, rect));
  }

  const st = useCanvasStore.getState();
  const source = st.nodes.find((n) => n.id === opts.sourceId);
  if (!source) throw new Error('源节点不存在');
  // 各格宽高只差取整的 ±1px，显示尺寸统一按标称格子宽高比算，排出来整齐。
  const size = cellNodeSize(img.naturalWidth / opts.cols / (img.naturalHeight / opts.rows));
  const ids = st.addLinkedInputImageNodes(
    opts.sourceId,
    rects.map((r) => ({ ...cellNodePosition(source, r.row, r.col, size), ...size })),
  );
  if (ids.length === 0) return { created: 0, failed: 0 };

  // 并发 3 上传：25 格全开会同时压 25 个上传请求；串行 25 格又太慢。
  let next = 0;
  let failed = 0;
  const worker = async () => {
    for (;;) {
      const i = next++;
      if (i >= ids.length) return;
      const ok = await uploadFileIntoNode(ids[i], files[i], { autoFittedFor: undefined });
      if (!ok) failed += 1;
    }
  };
  await Promise.all(Array.from({ length: Math.min(3, ids.length) }, worker));
  return { created: ids.length, failed };
}

function loadImage(url: string): Promise<HTMLImageElement> {
  const img = new Image();
  const co = mediaCrossOrigin(url);
  if (co) img.crossOrigin = co;
  img.src = url;
  return img
    .decode()
    .then(() => img)
    .catch(() => {
      throw new Error('原图加载失败，请稍后重试');
    });
}

function cropCell(img: HTMLImageElement, rect: CellRect): Promise<File> {
  const canvas = document.createElement('canvas');
  canvas.width = rect.width;
  canvas.height = rect.height;
  const ctx = canvas.getContext('2d');
  if (!ctx) return Promise.reject(new Error('当前浏览器不支持画布裁剪'));
  ctx.drawImage(img, rect.x, rect.y, rect.width, rect.height, 0, 0, rect.width, rect.height);
  return new Promise((resolve, reject) => {
    try {
      canvas.toBlob((blob) => {
        if (!blob) {
          reject(new Error('裁剪导出失败'));
          return;
        }
        // 文件名用 1 基行列号，素材库里能看出来自哪个格子。
        resolve(
          new File([blob], `grid-${rect.row + 1}-${rect.col + 1}.png`, { type: 'image/png' }),
        );
      }, 'image/png');
    } catch {
      // 跨域污染的 canvas 在 toBlob 上同步抛 SecurityError。
      reject(new Error('该图片源不允许读取像素（跨域限制），无法切分'));
    }
  });
}
