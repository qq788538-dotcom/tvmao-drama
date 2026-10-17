// 视频剪辑(trim)的媒体处理层:抽缩略图条 + 浏览器内转码截取选区。
// 全部基于 mediabunny(WebCodecs)。纯函数,不碰 store。
//
// mediabunny 用动态 import:它只在用户点「剪辑」后才需要,静态引入会把
// ~200KB 的编解码逻辑打进首屏 chunk。

// 最小选区时长(秒)。太短的选区转码出来只有一两帧,没有意义。
export const MIN_TRIM_DURATION = 0.5;

// WebCodecs 可用性探测。jsdom / 旧浏览器(Safari<16.4 等)没有 VideoEncoder,
// 此时「剪辑」入口直接报不支持,不进入 trim 模式。
export function isTrimSupported(): boolean {
  return typeof (globalThis as { VideoEncoder?: unknown }).VideoEncoder !== 'undefined';
}

// 把选区钳制到合法范围:[0, duration] 内、且至少 MIN_TRIM_DURATION 长。
// 拖拽把手/平移选区共用,导出以便单测钉住边界行为。
export function clampSelection(
  start: number,
  end: number,
  duration: number,
  min = MIN_TRIM_DURATION,
): { start: number; end: number } {
  const s = Math.min(Math.max(start, 0), Math.max(duration - min, 0));
  const e = Math.max(Math.min(end, duration), Math.min(s + min, duration));
  return { start: s, end: e };
}

// 键盘平移选区:保持时长整体挪 dt 秒,钳在 [0, duration]。
export function moveSelection(
  sel: { start: number; end: number },
  dt: number,
  duration: number,
): { start: number; end: number } {
  const len = sel.end - sel.start;
  const start = Math.min(Math.max(sel.start + dt, 0), Math.max(duration - len, 0));
  return { start, end: start + len };
}

// 键盘扩展(dt>0)/收缩(dt<0)选区:两端对称各动 dt 秒,复用 clampSelection
// 保证不越界、不短于最小时长。
export function resizeSelection(
  sel: { start: number; end: number },
  dt: number,
  duration: number,
  min = MIN_TRIM_DURATION,
): { start: number; end: number } {
  return clampSelection(sel.start - dt, sel.end + dt, duration, min);
}

// 拖拽把手/平移的选区计算。snap=整数秒吸附:把被拖动的那一端(平移时是起点)
// 吸到最近的整数秒,再走边界钳制——先吸后钳,吸出界的值仍会被拉回合法范围。
// 从组件 onMove 里抽出来是为了让吸附行为可单测(pointer 事件在 jsdom 里没法拖)。
export function dragSelection(
  kind: 'left' | 'right' | 'move',
  sel0: { start: number; end: number },
  dt: number,
  duration: number,
  snap: boolean,
  min = MIN_TRIM_DURATION,
): { start: number; end: number } {
  const snapTo = (v: number) => (snap ? Math.round(v) : v);
  if (kind === 'left') {
    return clampSelection(Math.min(snapTo(sel0.start + dt), sel0.end - min), sel0.end, duration, min);
  }
  if (kind === 'right') {
    return clampSelection(sel0.start, Math.max(snapTo(sel0.end + dt), sel0.start + min), duration, min);
  }
  return moveSelection(sel0, snapTo(sel0.start + dt) - sel0.start, duration);
}

export interface FilmstripResult {
  duration: number;
  // dataURL jpeg 小图,按时间升序;个别帧解不出来时对应位置是空串(UI 显示占位底色)。
  frames: string[];
}

// WrappedCanvas.canvas 可能是 OffscreenCanvas(没有 toDataURL),统一画到
// 一个普通 canvas 再导出,两种来源走同一条路。
function canvasToDataURL(source: HTMLCanvasElement | OffscreenCanvas): string {
  const el = document.createElement('canvas');
  el.width = source.width;
  el.height = source.height;
  el.getContext('2d')!.drawImage(source, 0, 0);
  return el.toDataURL('image/jpeg', 0.7);
}

// 均匀抽 count 帧做裁剪条缩略图。取每格中点时刻((i+0.5)/count),
// 首尾帧各自贴边采样在短视频上容易黑帧/重复帧。
export async function extractFilmstrip(url: string, count: number): Promise<FilmstripResult> {
  const { Input, UrlSource, ALL_FORMATS, CanvasSink } = await import('mediabunny');
  const input = new Input({ formats: ALL_FORMATS, source: new UrlSource(url) });
  // 无论成败都 dispose:出错路径上不关的话,打开的 VideoDecoder(硬件解码槽位
  // 有限)和 UrlSource 的在途请求要等 GC 才释放。下同。
  try {
    const track = await input.getPrimaryVideoTrack();
    if (!track) throw new Error('视频里没有可用的视频轨');
    const duration = await input.computeDuration();
    // fit:'cover' 裁满格子,竖屏视频不留黑边(缩略图只要认得出画面,无需完整构图)。
    const sink = new CanvasSink(track, { width: 96, height: 56, fit: 'cover' });
    const timestamps = Array.from({ length: count }, (_, i) => ((i + 0.5) / count) * duration);
    const frames: string[] = [];
    for await (const wrapped of sink.canvasesAtTimestamps(timestamps)) {
      frames.push(wrapped ? canvasToDataURL(wrapped.canvas) : '');
    }
    return { duration, frames };
  } finally {
    input.dispose();
  }
}

// 截取 [start, end] 转码出 mp4 Blob。mediabunny 的 Conversion 在容器/编码
// 兼容时对未触及的部分走 stream copy,常见的 mp4→mp4 剪辑秒级完成。
// onProgress 收 0~1。
export async function trimVideo(
  url: string,
  start: number,
  end: number,
  onProgress?: (progress: number) => void,
): Promise<Blob> {
  const { Input, Output, Conversion, UrlSource, ALL_FORMATS, BufferTarget, Mp4OutputFormat } =
    await import('mediabunny');
  const input = new Input({ formats: ALL_FORMATS, source: new UrlSource(url) });
  try {
    const target = new BufferTarget();
    const output = new Output({
      // fastStart:'in-memory' 把 moov 放文件头,产物上传后可边下边播。
      format: new Mp4OutputFormat({ fastStart: 'in-memory' }),
      target,
    });
    const conversion = await Conversion.init({
      input,
      output,
      trim: { start, end },
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
