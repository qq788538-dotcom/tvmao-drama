// 宫格切分的纯计算侧（可单测，不碰 DOM/store）：像素网格划分、裁片节点
// 显示尺寸、画布落点。交互与执行分别在 GridSplit.tsx / executeGridSplit.ts。

export interface GridPreset {
  rows: number;
  cols: number;
  label: string;
}

// 下拉菜单的四个固定档（对标 libtv 文案），自定义走 hover 选择器（上限同 5×5）。
export const GRID_PRESETS: GridPreset[] = [
  { rows: 2, cols: 2, label: '4宫格 (2×2)' },
  { rows: 3, cols: 3, label: '9宫格 (3×3)' },
  { rows: 4, cols: 4, label: '16宫格 (4×4)' },
  { rows: 5, cols: 5, label: '25宫格 (5×5)' },
];

export const MAX_CUSTOM_GRID = 5;

export interface CellRect {
  row: number;
  col: number;
  x: number;
  y: number;
  width: number;
  height: number;
}

/**
 * 把 imgW×imgH 划成 rows×cols 的像素矩形。边界用「累计边界取整」
 * （bounds[i] = round(img * i / n)）而不是固定步长：相邻边界之差恰好铺满全图
 * 不重不漏；固定步长在非整除时会让最后一行/列丢掉或多出几个像素。
 */
export function cellRects(imgW: number, imgH: number, rows: number, cols: number): CellRect[] {
  const xs = Array.from({ length: cols + 1 }, (_, i) => Math.round((imgW * i) / cols));
  const ys = Array.from({ length: rows + 1 }, (_, i) => Math.round((imgH * i) / rows));
  const rects: CellRect[] = [];
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < cols; c++) {
      rects.push({
        row: r,
        col: c,
        x: xs[c],
        y: ys[r],
        width: xs[c + 1] - xs[c],
        height: ys[r + 1] - ys[r],
      });
    }
  }
  return rects;
}

/**
 * 裁片节点的显示尺寸。**必须与 InputImageNode 的 auto-fit 公式逐字一致**
 * （targetArea 290²、下限 200）：上传完成后该节点的 auto-fit effect 会按缩略图
 * 宽高比再算一次尺寸，两处公式一致时重算结果与建节点时相同，宫格排布才不会在
 * 上传完成的瞬间集体跳动。
 */
export function cellNodeSize(aspect: number): { width: number; height: number } {
  const targetArea = 290 * 290;
  const h = Math.sqrt(targetArea / aspect);
  const w = h * aspect;
  return { width: Math.max(200, Math.round(w)), height: Math.max(200, Math.round(h)) };
}

// 相邻裁片间距 / 第一列距源节点右缘（世界单位）。
export const SPLIT_GAP = 48;
export const SPLIT_OFFSET_X = 160;

/**
 * 裁片在画布上的落点：源节点右侧铺开，行列位置照搬宫格里的行列——只切部分
 * 格子时留洞不紧凑排（1-1 和 2-2 摆成对角），一眼能看出每片来自原图哪个位置。
 */
export function cellNodePosition(
  source: { x: number; y: number; width: number },
  row: number,
  col: number,
  size: { width: number; height: number },
): { x: number; y: number } {
  return {
    x: source.x + source.width + SPLIT_OFFSET_X + col * (size.width + SPLIT_GAP),
    y: source.y + row * (size.height + SPLIT_GAP),
  };
}
