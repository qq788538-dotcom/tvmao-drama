// 标注保存的执行侧：加载原图 → 原图分辨率 canvas 上「底图 + 笔画重放」合成
// PNG → 建 input-image 节点（连回源）→ 上传。纯前端合成，零后端改动、零积分
// ——与宫格切分同性质（本地合成后直传对象存储 + 建新节点连线，
// 原节点不动）。
import { useCanvasStore } from '../../store';
import { uploadFileIntoNode } from '../../utils/uploadIntoNode';
import { mediaCrossOrigin } from '../../utils/mediaCrossOrigin';
import { cellNodeSize, SPLIT_OFFSET_X } from '../gridsplit/gridSplitMath';
import { replayStrokes, type AnnotateStroke } from './annotateMath';

/**
 * 加载待标注的原图。进入标注模式时就调（而不是等保存）：既拿 natural 尺寸
 * 建立坐标空间，也把跨域问题提前到落笔之前暴露——画完十笔才发现导不出来
 * 最伤。crossOrigin 语义同宫格切分（见 mediaCrossOrigin 注释）。
 */
export function loadAnnotateImage(url: string): Promise<HTMLImageElement> {
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

/**
 * 合成并落节点。成功返回新节点 id；上传失败返回 null（错误已由
 * uploadFileIntoNode 写在新节点 status/error 上）。合成失败（跨域污染等）
 * throw，调用方 toast 后留在标注模式里——笔画不能丢。
 */
export async function executeAnnotateSave(opts: {
  sourceId: string;
  image: HTMLImageElement;
  strokes: readonly AnnotateStroke[];
}): Promise<string | null> {
  const { image, strokes } = opts;
  const canvas = document.createElement('canvas');
  canvas.width = image.naturalWidth;
  canvas.height = image.naturalHeight;
  const ctx = canvas.getContext('2d');
  if (!ctx) throw new Error('当前浏览器不支持画布合成');
  ctx.drawImage(image, 0, 0);
  replayStrokes(ctx, strokes);
  const file = await toPngFile(canvas);

  const st = useCanvasStore.getState();
  const source = st.nodes.find((n) => n.id === opts.sourceId);
  if (!source) throw new Error('源节点不存在');
  // 合成图与原图同宽高比，显示尺寸公式同 InputImageNode auto-fit（经由
  // cellNodeSize），落点在源节点右侧（间距同宫格切分第一列）。
  const size = cellNodeSize(image.naturalWidth / image.naturalHeight);
  const [id] = st.addLinkedInputImageNodes(opts.sourceId, [
    { x: source.x + source.width + SPLIT_OFFSET_X, y: source.y, ...size },
  ]);
  if (!id) return null;
  const ok = await uploadFileIntoNode(id, file, { autoFittedFor: undefined });
  return ok ? id : null;
}

function toPngFile(canvas: HTMLCanvasElement): Promise<File> {
  return new Promise((resolve, reject) => {
    try {
      canvas.toBlob((blob) => {
        if (!blob) {
          reject(new Error('标注导出失败'));
          return;
        }
        resolve(new File([blob], `annotate-${Date.now()}.png`, { type: 'image/png' }));
      }, 'image/png');
    } catch {
      // 跨域污染的 canvas 在 toBlob 上同步抛 SecurityError。
      reject(new Error('该图片源不允许读取像素（跨域限制），无法保存标注'));
    }
  });
}
