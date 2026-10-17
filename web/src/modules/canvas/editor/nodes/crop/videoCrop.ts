// 视频空间裁剪(crop)核心——工具条「裁剪」按钮的转码层与几何层,是
// videoTrim.ts 的空间版。全部基于 mediabunny(WebCodecs)。
//
// 与 trim 的关键差异:改了像素必然全程重编码,不存在 mp4→mp4 stream copy
// 快路径,8s 720p 也要数秒——进度回调不是锦上添花而是必须。
//
// 几何全部在「分数空间」做(rect 四值都是相对源视频画面的 0~1 分数):
// 拖拽换算、clamp、锁比例都与像素无关,只在显示标签和最终提交时换算像素。
// 这样单测不用造 DOM,直接喂分数断分数。

// 相对源视频画面的分数矩形(0~1)。
export interface FracRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

// mediabunny CropRectangle 同构:源视频显示像素空间。
export interface PixelRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

export type HandleKind = 'nw' | 'n' | 'ne' | 'e' | 'se' | 's' | 'sw' | 'w' | 'move';

// 最小裁剪框(源视频像素)。防误拖出无意义产物;
// 64 取自「再小编码器也稳、缩略图也还认得出内容」的经验值。
export const MIN_CROP_PX = 64;

// 初始框:居中 80%(left/top 10% + 80%×80%)。
export const INITIAL_RECT: FracRect = { left: 0.1, top: 0.1, width: 0.8, height: 0.8 };

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

// 四舍五入到最近偶数——WebCodecs 编码器(H.264)要求偶数尺寸,
// 产物/标签因此全是偶数。
export function roundEven(n: number): number {
  return Math.round(n / 2) * 2;
}

// 分数 rect → 源视频像素 rect:尺寸 round-to-even,再整体 clamp 回画面内
// (round 可能把 right/bottom 顶出边界 2px 以内)。left/top 取整即可,
// 编码器只对宽高挑偶数。
export function toPixelRect(rect: FracRect, videoW: number, videoH: number): PixelRect {
  const width = clamp(roundEven(rect.width * videoW), MIN_CROP_PX, roundEven(videoW));
  const height = clamp(roundEven(rect.height * videoH), MIN_CROP_PX, roundEven(videoH));
  const left = clamp(Math.round(rect.left * videoW), 0, videoW - width);
  const top = clamp(Math.round(rect.top * videoH), 0, videoH - height);
  return { left, top, width, height };
}

// 框身平移:尺寸不变,位置 clamp 在画面内。dx/dy 是分数增量。
export function moveRect(r: FracRect, dx: number, dy: number): FracRect {
  return {
    ...r,
    left: clamp(r.left + dx, 0, 1 - r.width),
    top: clamp(r.top + dy, 0, 1 - r.height),
  };
}

// 手柄拖拽规则:角手柄锁比例、边手柄单轴自由。
// dx/dy 是从 pointerdown 起的累计分数增量,r 是拖拽起点的 rect(不是上一帧
// 的——增量算法在快速拖动下会漂移,绝对算法怎么拖都可逆)。
// minW/minH 是分数化的最小框(MIN_CROP_PX / 视频像素)。
export function resizeRect(
  r: FracRect,
  kind: Exclude<HandleKind, 'move'>,
  dx: number,
  dy: number,
  minW: number,
  minH: number,
): FracRect {
  const right = r.left + r.width;
  const bottom = r.top + r.height;

  // 边手柄:动的那条边 clamp 在 [对边+最小尺寸, 画面边界]。
  if (kind === 'e') {
    const w = clamp(r.width + dx, minW, 1 - r.left);
    return { ...r, width: w };
  }
  if (kind === 's') {
    const h = clamp(r.height + dy, minH, 1 - r.top);
    return { ...r, height: h };
  }
  if (kind === 'w') {
    const left = clamp(r.left + dx, 0, right - minW);
    return { ...r, left, width: right - left };
  }
  if (kind === 'n') {
    const top = clamp(r.top + dy, 0, bottom - minH);
    return { ...r, top, height: bottom - top };
  }

  // 角手柄:锁比例缩放,锚点是对角。缩放系数取主导轴(位移绝对值大的那根),
  // 次轴跟随——角拖时宽高分数同步变。
  // 系数 clamp 三重约束:不小于最小框、不越出锚点到画面边界的可用空间。
  const sx = kind === 'ne' || kind === 'se' ? (r.width + dx) / r.width : (r.width - dx) / r.width;
  const sy = kind === 'sw' || kind === 'se' ? (r.height + dy) / r.height : (r.height - dy) / r.height;
  let s = Math.abs(dx) >= Math.abs(dy) ? sx : sy;
  const maxSx = (kind === 'nw' || kind === 'sw' ? right : 1 - r.left) / r.width;
  const maxSy = (kind === 'nw' || kind === 'ne' ? bottom : 1 - r.top) / r.height;
  s = clamp(s, Math.max(minW / r.width, minH / r.height), Math.min(maxSx, maxSy));
  const w = r.width * s;
  const h = r.height * s;
  return {
    left: kind === 'nw' || kind === 'sw' ? right - w : r.left,
    top: kind === 'nw' || kind === 'ne' ? bottom - h : r.top,
    width: w,
    height: h,
  };
}

// 裁出 rect(源视频像素) 转码出 mp4 Blob。与 trimVideo 同构,trim 换 crop;
// 音频轨 Conversion 默认原样带过,不用管。onProgress 收 0~1。
export async function cropVideo(
  url: string,
  rect: PixelRect,
  onProgress?: (progress: number) => void,
): Promise<Blob> {
  const { Input, Output, Conversion, UrlSource, ALL_FORMATS, BufferTarget, Mp4OutputFormat } =
    await import('mediabunny');
  const input = new Input({ formats: ALL_FORMATS, source: new UrlSource(url) });
  // 无论成败都 dispose(同 trimVideo)。
  try {
    const target = new BufferTarget();
    const output = new Output({
      // fastStart:'in-memory' 把 moov 放文件头,产物上传后可边下边播(同 trim)。
      format: new Mp4OutputFormat({ fastStart: 'in-memory' }),
      target,
    });
    const conversion = await Conversion.init({
      input,
      output,
      video: { crop: rect },
      showWarnings: false,
    });
    if (onProgress) conversion.onProgress = (p) => onProgress(p);
    await conversion.execute();
    if (!target.buffer) throw new Error('转码未产出数据');
    return new Blob([target.buffer], { type: 'video/mp4' });
  } finally {
    input.dispose();
  }
}
