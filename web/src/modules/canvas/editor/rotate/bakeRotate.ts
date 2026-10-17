// 旋转与镜像的烘焙侧：把原图按角度+镜像重绘成一张新 PNG。纯前端、零后端、
// 零积分——产物走统一上传口变成普通图片素材，下游生成拿到的就是转正后的像素
// （结果节点存的是烘焙图，不是「角度属性」）。
//
// 非 90° 倍数角度旋转后外接矩形变大，外扩区域保持透明：PNG 透明底
// 不引入假像素，下游模型对透明区域的处理由模型自己决定。
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';
import { rotatedBox } from './rotateMath';

/** 与 executeGridSplit.loadImage 同款（各自 10 行，场景注释不同，不抽公共）。 */
export function loadImage(url: string): Promise<HTMLImageElement> {
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
 * 变换顺序钉死为「先镜像、后旋转」，且必须与预览的 CSS
 * `rotate(angle) scale(sx, sy)` 一致（CSS transform 右侧先作用）——两边顺序
 * 不一致时，镜像+非对称角度的组合会出现预览与保存结果互为镜像的鬼问题。
 */
export function bakeRotateMirror(
  img: HTMLImageElement,
  angleDeg: number,
  flipH: boolean,
  flipV: boolean,
): Promise<File> {
  const w = img.naturalWidth;
  const h = img.naturalHeight;
  const box = rotatedBox(w, h, angleDeg);
  const canvas = document.createElement('canvas');
  canvas.width = box.width;
  canvas.height = box.height;
  const ctx = canvas.getContext('2d');
  if (!ctx) return Promise.reject(new Error('当前浏览器不支持画布旋转'));
  ctx.translate(box.width / 2, box.height / 2);
  ctx.rotate((angleDeg * Math.PI) / 180);
  ctx.scale(flipH ? -1 : 1, flipV ? -1 : 1);
  ctx.drawImage(img, -w / 2, -h / 2);
  return new Promise((resolve, reject) => {
    try {
      canvas.toBlob((blob) => {
        if (!blob) {
          reject(new Error('旋转导出失败'));
          return;
        }
        resolve(new File([blob], `rotate-${angleDeg}.png`, { type: 'image/png' }));
      }, 'image/png');
    } catch {
      // 跨域污染的 canvas 在 toBlob 上同步抛 SecurityError（历史节点可能落了
      // 不带 CORS 头的上游裸 URL）。
      reject(new Error('该图片源不允许读取像素（跨域限制），无法旋转'));
    }
  });
}
