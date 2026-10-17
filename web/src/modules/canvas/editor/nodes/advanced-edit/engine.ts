// 标注引擎：矢量 op 模型 + 撤销重做 + 命中擦除 + 渲染合成。
// 坐标一律存视频原始像素空间（naturalWidth/Height）——显示层缩放、合成层 1:1，
// 只在指针落点处换算一次，后续所有环节零换算（换算散在多处迟早对不齐）。
// 全部纯函数返回新对象：React 靠引用变化触发重绘，原地改会画不出来。

export type AnnoTool = 'rect' | 'pen' | 'arrow' | 'text' | 'eraser' | 'pin';

export interface AnnoOp {
  id: string;
  tool: Exclude<AnnoTool, 'eraser'>; // eraser 是动作不是产物
  points: Array<{ x: number; y: number }>; // rect/arrow: [起,终]; pen: 折线; text/pin: [位置]
  text?: string; // text 工具的内容
  color?: string; // 标注颜色；缺省 = DEFAULT_ANNO_COLOR（老 op 无此字段照常红色）
  // 落笔那一刻的「原始像素/屏幕像素」换算比。样式统一的基准是**屏幕像素**
  // （线宽 3 屏幕px、文字 15px、图钉 9px，任何分辨率的视频观感一致——用户拍板），
  // 渲染/合成时用它把屏幕基准换回原始空间，保存出的标注帧与屏幕所见比例完全
  // 一致（所见即所得）。缺省（老 op）退回按帧宽推导的旧算法。
  px?: number;
}

// 屏幕像素基准的统一样式常量：所有工具、所有视频分辨率共用一套观感。
export const STROKE_DISPLAY_PX = 3;
export const FONT_DISPLAY_PX = 15;
export const PIN_RADIUS_DISPLAY_PX = 9;

// 调色板（六色）。白色排最后——深色画面用白、浅色画面用黑，
// 两个极端色都得有，否则总有画面标不清楚。
export const ANNO_COLORS = ['#ff3b30', '#ffcc00', '#34c759', '#0a84ff', '#111111', '#ffffff'] as const;
export const DEFAULT_ANNO_COLOR: string = ANNO_COLORS[0];

export interface FrameAnnotation {
  // 记录唯一 id：同一秒可以有多条独立记录（每次「添加至输入框」都新增，用户
  // 拍板），React key/说明编辑/删除/抓帧全部按 id 定位——按 timeSec 定位会让
  // 同秒的多条互相串（改一条说明全变、抓帧全合成第一条的涂鸦）。
  id: string;
  timeSec: number;
  ops: AnnoOp[];
  redoStack: AnnoOp[];
  note: string;          // 该时间点的说明文字（进提示词）
  thumbDataUrl?: string; // 记录条缩略图（本地 base64，仅展示）
}

let frameSeq = 0;
export function newFrame(timeSec: number): FrameAnnotation {
  frameSeq += 1;
  return { id: `f-${frameSeq}`, timeSec, ops: [], redoStack: [], note: '' };
}

export function pushOp(f: FrameAnnotation, op: AnnoOp): FrameAnnotation {
  // 新笔画作废旧的重做分支——保留会让 redo 把不相干的历史画回来。
  return { ...f, ops: [...f.ops, op], redoStack: [] };
}

export function undo(f: FrameAnnotation): FrameAnnotation {
  if (f.ops.length === 0) return f;
  return { ...f, ops: f.ops.slice(0, -1), redoStack: [...f.redoStack, f.ops[f.ops.length - 1]] };
}

export function redo(f: FrameAnnotation): FrameAnnotation {
  if (f.redoStack.length === 0) return f;
  const op = f.redoStack[f.redoStack.length - 1];
  return { ...f, ops: [...f.ops, op], redoStack: f.redoStack.slice(0, -1) };
}

// 点到线段距离——eraser 与 pen/arrow/rect 边的命中判定共用。
function segDist(px: number, py: number, x1: number, y1: number, x2: number, y2: number): number {
  const dx = x2 - x1, dy = y2 - y1;
  const len2 = dx * dx + dy * dy;
  const t = len2 === 0 ? 0 : Math.max(0, Math.min(1, ((px - x1) * dx + (py - y1) * dy) / len2));
  return Math.hypot(px - (x1 + t * dx), py - (y1 + t * dy));
}

function opHit(op: AnnoOp, x: number, y: number, threshold: number): boolean {
  const pts = op.points;
  switch (op.tool) {
    case 'rect': {
      // 命中四条边而不是填充区——框通常框住主体，点内部多半是想擦别的。
      const [a, b] = pts;
      const xs = [a.x, b.x], ys = [a.y, b.y];
      const [x1, x2] = [Math.min(...xs), Math.max(...xs)];
      const [y1, y2] = [Math.min(...ys), Math.max(...ys)];
      return (
        segDist(x, y, x1, y1, x2, y1) < threshold || segDist(x, y, x1, y2, x2, y2) < threshold ||
        segDist(x, y, x1, y1, x1, y2) < threshold || segDist(x, y, x2, y1, x2, y2) < threshold
      );
    }
    case 'pen':
    case 'arrow': {
      for (let i = 0; i < pts.length - 1; i++) {
        if (segDist(x, y, pts[i].x, pts[i].y, pts[i + 1].x, pts[i + 1].y) < threshold) return true;
      }
      return pts.length === 1 && Math.hypot(x - pts[0].x, y - pts[0].y) < threshold;
    }
    case 'text':
    case 'pin':
      return Math.hypot(x - pts[0].x, y - pts[0].y) < threshold * 3; // 点状物给大些的判定圈
  }
}

export function eraseAt(f: FrameAnnotation, x: number, y: number, threshold: number): FrameAnnotation {
  const idx = [...f.ops].reverse().findIndex((op) => opHit(op, x, y, threshold));
  if (idx < 0) return f;
  const realIdx = f.ops.length - 1 - idx; // 后画的在上层，优先擦
  return { ...f, ops: f.ops.filter((_, i) => i !== realIdx), redoStack: [] };
}

// 老 op（无烘焙 px）的兜底样式：按帧宽推导。新 op 一律走屏幕像素基准。
function legacyStyleFor(w: number) {
  return { lineWidth: Math.max(3, Math.round(w / 320)), font: Math.max(18, Math.round(w / 36)) };
}

// 文字/图钉序号的衬底色：浅色标注用深衬底、深色标注用浅衬底，保证任何画面可读。
function contrastFor(color: string): string {
  return color === '#ffffff' || color === '#ffcc00' ? '#111111' : '#ffffff';
}

export function renderOps(ctx: CanvasRenderingContext2D, ops: AnnoOp[]): void {
  const legacy = legacyStyleFor(ctx.canvas.width);
  let pinSeq = 0;
  for (const op of ops) {
    // 屏幕像素基准 × 落笔时烘焙的换算比 = 原始空间下的实际粗细。用户的视频
    // 分辨率千差万别（480p 到 4K），按帧宽算会导致小窗口里线细得看不见——
    // 统一观感的锚点只能是屏幕像素。
    const s = op.px
      ? { lineWidth: Math.max(1.5, STROKE_DISPLAY_PX * op.px), font: Math.max(10, FONT_DISPLAY_PX * op.px) }
      : legacy;
    const pinR = op.px ? PIN_RADIUS_DISPLAY_PX * op.px : legacy.font * 0.7;
    const color = op.color ?? DEFAULT_ANNO_COLOR;
    ctx.strokeStyle = color;
    ctx.fillStyle = color;
    ctx.lineWidth = s.lineWidth;
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';
    const pts = op.points;
    switch (op.tool) {
      case 'rect': {
        const [a, b] = pts;
        ctx.strokeRect(Math.min(a.x, b.x), Math.min(a.y, b.y), Math.abs(b.x - a.x), Math.abs(b.y - a.y));
        break;
      }
      case 'pen': {
        ctx.beginPath();
        pts.forEach((p, i) => (i === 0 ? ctx.moveTo(p.x, p.y) : ctx.lineTo(p.x, p.y)));
        ctx.stroke();
        break;
      }
      case 'arrow': {
        const [a, b] = pts;
        ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y); ctx.stroke();
        const ang = Math.atan2(b.y - a.y, b.x - a.x);
        const head = s.lineWidth * 5;
        ctx.beginPath();
        ctx.moveTo(b.x, b.y);
        ctx.lineTo(b.x - head * Math.cos(ang - 0.5), b.y - head * Math.sin(ang - 0.5));
        ctx.lineTo(b.x - head * Math.cos(ang + 0.5), b.y - head * Math.sin(ang + 0.5));
        ctx.closePath(); ctx.fill();
        break;
      }
      case 'text': {
        ctx.font = `bold ${s.font}px sans-serif`;
        ctx.strokeStyle = contrastFor(color);
        ctx.lineWidth = Math.max(2, s.lineWidth - 1);
        ctx.strokeText(op.text ?? '', pts[0].x, pts[0].y); // 反差描边保证任何画面可读
        ctx.fillText(op.text ?? '', pts[0].x, pts[0].y);
        break;
      }
      case 'pin': {
        pinSeq += 1;
        const r = pinR;
        ctx.beginPath(); ctx.arc(pts[0].x, pts[0].y, r, 0, Math.PI * 2); ctx.fill();
        ctx.fillStyle = contrastFor(color);
        ctx.font = `bold ${s.font * 0.8}px sans-serif`;
        ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
        ctx.fillText(String(pinSeq), pts[0].x, pts[0].y);
        ctx.textAlign = 'start'; ctx.textBaseline = 'alphabetic';
        break;
      }
    }
  }
}

// 合成标注帧：视频帧铺底 + 标注绘制。ops 坐标在**源（视频原始）像素空间**——
// 输出尺寸与源一致时（保存全帧）1:1 直绘；不一致时（缩略图）必须按比例缩放
// 绘制，否则原始坐标全落在小画布之外，缩略图看起来「没有涂鸦」（实机踩中）。
export function composeFrame(
  source: CanvasImageSource, ops: AnnoOp[], width: number, height: number,
  srcWidth: number = width, srcHeight: number = height,
): HTMLCanvasElement {
  const canvas = document.createElement('canvas');
  canvas.width = width; canvas.height = height;
  const ctx = canvas.getContext('2d');
  if (ctx) {
    ctx.drawImage(source, 0, 0, width, height);
    ctx.save();
    ctx.scale(width / srcWidth, height / srcHeight);
    renderOps(ctx, ops);
    ctx.restore();
  }
  return canvas;
}

export function formatTime(sec: number): string {
  const s = Math.floor(sec);
  return `${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`;
}
