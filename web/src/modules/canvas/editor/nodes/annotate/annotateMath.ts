// 图片标注的纯计算侧（可单测，不碰 DOM/store）：坐标空间换算与笔画重放。
// 交互与执行分别在 AnnotateMode.tsx / executeAnnotateSave.ts。
//
// 核心约定：**所有笔画一律存原图像素空间**（对标 liblib 实测：它绘制期用
// 显示分辨率、导出时放大重放；我们在采集时就换算到原图空间，效果相同，但
// 画布 zoom 中途变化不影响已存笔画，导出重放零二次换算）。预览与导出走同
// 一个 replayStrokes——两边只差 ctx 的 transform，不可能画得不一样。

// 7 个固定标注色，顺序与默认值照抄 liblib 实测（黄/橙/粉/红/紫/蓝/白，默认红）。
export const ANNOTATE_COLORS = [
  '#facc15',
  '#f97316',
  '#ec4899',
  '#ef4444',
  '#a855f7',
  '#3b82f6',
  '#ffffff',
] as const;
export const DEFAULT_ANNOTATE_COLOR = '#ef4444';

// 线宽滑杆的档位范围与默认值（对标 liblib：默认 4）。滑杆值的语义是「节点
// 内容区世界单位下的像素」——世界单位不随画布 zoom 变，同一档位在任何缩放
// 下导出的观感一致。
export const MIN_STROKE_WIDTH = 2;
export const MAX_STROKE_WIDTH = 20;
export const DEFAULT_STROKE_WIDTH = 4;

// 文字字号 = 线宽滑杆值 × 固定倍率（liblib 亦是线宽驱动字号，无独立字号控件）。
export const TEXT_FONT_RATIO = 6;

export type AnnotateStroke =
  | { kind: 'pen'; color: string; width: number; points: Array<{ x: number; y: number }> }
  | { kind: 'rect'; color: string; width: number; x: number; y: number; w: number; h: number }
  | { kind: 'text'; color: string; fontSize: number; x: number; y: number; text: string };

export interface FitRect {
  x: number;
  y: number;
  width: number;
  height: number;
  /** 显示像素 → 原图像素的放大系数（naturalWidth / fit.width）。 */
  scale: number;
}

/**
 * object-contain 的适配矩形：naturalW×naturalH 的图在 boxW×boxH 的盒子里
 * 等比缩放居中后占据的区域（盒子坐标系）。节点虽然会 auto-fit 到图片宽高比，
 * 但手动拉过节点、或 auto-fit 尚未跑完时两者不一致，信这里而不是信节点。
 */
export function fitRect(
  naturalW: number,
  naturalH: number,
  boxW: number,
  boxH: number,
): FitRect {
  const s = Math.min(boxW / naturalW, boxH / naturalH);
  const width = naturalW * s;
  const height = naturalH * s;
  return {
    x: (boxW - width) / 2,
    y: (boxH - height) / 2,
    width,
    height,
    scale: naturalW / width,
  };
}

/** 盒子坐标（如 overlay 内的 offsetX/Y）→ 原图像素坐标，越界钳到图内。 */
export function toNatural(
  px: number,
  py: number,
  fit: FitRect,
  naturalW: number,
  naturalH: number,
): { x: number; y: number } {
  return {
    x: Math.min(naturalW, Math.max(0, (px - fit.x) * fit.scale)),
    y: Math.min(naturalH, Math.max(0, (py - fit.y) * fit.scale)),
  };
}

/**
 * 线宽滑杆值（世界单位）→ 原图像素线宽。fit.scale 是「显示像素→原图像素」
 * 系数、zoom 是采集瞬间的画布缩放，两者相乘 = naturalW / 图片世界宽度——
 * zoom 从乘积里消掉，所以同一滑杆档位在任何缩放下换算出的原图线宽相同。
 */
export function naturalStrokeWidth(sliderValue: number, fitScale: number, zoom: number): number {
  return sliderValue * fitScale * zoom;
}

/** 文字草稿定格为笔画：空白内容丢弃（返回原数组，方便调用方 === 判断没变）。 */
export function commitTextDraft(
  strokes: AnnotateStroke[],
  draft: { x: number; y: number; text: string; color: string; fontSize: number },
): AnnotateStroke[] {
  if (!draft.text.trim()) return strokes;
  return [
    ...strokes,
    {
      kind: 'text',
      color: draft.color,
      fontSize: draft.fontSize,
      x: draft.x,
      y: draft.y,
      text: draft.text,
    },
  ];
}

/**
 * 把笔画重放到 ctx 上。坐标全在原图像素空间——预览时调用方先
 * setTransform(把原图空间投到画布)，导出时恒等变换直接画，两边同一份代码。
 */
export function replayStrokes(
  ctx: CanvasRenderingContext2D,
  strokes: readonly AnnotateStroke[],
): void {
  for (const s of strokes) {
    ctx.strokeStyle = s.kind === 'text' ? 'transparent' : s.color;
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';
    if (s.kind === 'pen') {
      if (s.points.length === 0) continue;
      ctx.lineWidth = s.width;
      ctx.beginPath();
      ctx.moveTo(s.points[0].x, s.points[0].y);
      for (let i = 1; i < s.points.length; i++) ctx.lineTo(s.points[i].x, s.points[i].y);
      // 单点落笔也要可见：画一段零长线，round cap 会渲染成圆点。
      if (s.points.length === 1) ctx.lineTo(s.points[0].x, s.points[0].y);
      ctx.stroke();
    } else if (s.kind === 'rect') {
      ctx.lineWidth = s.width;
      ctx.strokeRect(s.x, s.y, s.w, s.h);
    } else {
      ctx.fillStyle = s.color;
      ctx.font = `${s.fontSize}px sans-serif`;
      ctx.textBaseline = 'top';
      // 多行文字按行画（textarea 里可回车换行），行高 1.25 倍字号。
      const lines = s.text.split('\n');
      for (let i = 0; i < lines.length; i++) {
        ctx.fillText(lines[i], s.x, s.y + i * s.fontSize * 1.25);
      }
    }
  }
}
