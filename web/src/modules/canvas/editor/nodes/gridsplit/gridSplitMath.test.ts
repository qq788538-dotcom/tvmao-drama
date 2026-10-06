// 宫格切分纯计算的约定钉死在这里：cellRects 用「累计边界取整」保证非整除
// 尺寸下不重不漏铺满全图；cellNodeSize 必须与 InputImageNode 的 auto-fit 公式
// （targetArea 290²、下限 200）逐字一致——上传完成后 InputImageNode 会按缩略图
// 宽高比重算一次尺寸，两处公式若有偏差，重算结果与建节点时不同，宫格排布会在
// 上传完成的瞬间集体跳动；cellNodePosition 按宫格行列在源节点右侧留洞铺开，
// 间距/偏移只认导出的 SPLIT_GAP / SPLIT_OFFSET_X 常量。
import { describe, expect, it } from 'vitest';
import {
  cellNodePosition,
  cellNodeSize,
  cellRects,
  SPLIT_GAP,
  SPLIT_OFFSET_X,
} from './gridSplitMath';

describe('cellRects', () => {
  it('2048×1152 切 3×3（非整除）：9 格铺满全图、边界严丝合缝、无负数', () => {
    const rects = cellRects(2048, 1152, 3, 3);
    expect(rects).toHaveLength(9);

    // 每行 width 之和 = 全图宽；每列 height 之和 = 全图高。
    for (let r = 0; r < 3; r++) {
      const rowWidth = rects
        .filter((rect) => rect.row === r)
        .reduce((sum, rect) => sum + rect.width, 0);
      expect(rowWidth).toBe(2048);
    }
    for (let c = 0; c < 3; c++) {
      const colHeight = rects
        .filter((rect) => rect.col === c)
        .reduce((sum, rect) => sum + rect.height, 0);
      expect(colHeight).toBe(1152);
    }

    // 相邻格边界严丝合缝：右邻起点 = 前一格 x+width，下邻起点 = 前一格 y+height。
    const at = (r: number, c: number) => rects.find((rect) => rect.row === r && rect.col === c)!;
    for (let r = 0; r < 3; r++) {
      for (let c = 0; c < 2; c++) {
        expect(at(r, c + 1).x).toBe(at(r, c).x + at(r, c).width);
      }
    }
    for (let c = 0; c < 3; c++) {
      for (let r = 0; r < 2; r++) {
        expect(at(r + 1, c).y).toBe(at(r, c).y + at(r, c).height);
      }
    }

    // 全部字段非负（固定步长实现在非整除时可能出负/零尺寸尾格）。
    for (const rect of rects) {
      expect(rect.x).toBeGreaterThanOrEqual(0);
      expect(rect.y).toBeGreaterThanOrEqual(0);
      expect(rect.width).toBeGreaterThan(0);
      expect(rect.height).toBeGreaterThan(0);
    }
  });

  it('100×100 切 2×2：每格恰好 50×50', () => {
    const rects = cellRects(100, 100, 2, 2);
    expect(rects).toHaveLength(4);
    for (const rect of rects) {
      expect(rect.width).toBe(50);
      expect(rect.height).toBe(50);
      expect(rect.x).toBe(rect.col * 50);
      expect(rect.y).toBe(rect.row * 50);
    }
  });
});

describe('cellNodeSize', () => {
  it('与 InputImageNode auto-fit 公式一致：aspect=16/9 时 h=sqrt(290²/aspect)、w=h*aspect', () => {
    const aspect = 16 / 9;
    // 独立照 InputImageNode 的公式手算一遍，round 后必须与被测函数逐位相等。
    const expectedH = Math.sqrt((290 * 290) / aspect);
    const expectedW = expectedH * aspect;
    const size = cellNodeSize(aspect);
    expect(size.width).toBe(Math.round(expectedW));
    expect(size.height).toBe(Math.round(expectedH));
    // 16:9 不该碰到 200 下限——碰到说明公式被改动过。
    expect(size.width).toBeGreaterThan(200);
    expect(size.height).toBeGreaterThan(200);
  });

  it('极端窄高比（aspect=0.1）触发 200 宽度下限', () => {
    const size = cellNodeSize(0.1);
    expect(size.width).toBe(200);
    // 高度按公式正常算：sqrt(290²/0.1) ≈ 917。
    expect(size.height).toBe(Math.round(Math.sqrt((290 * 290) / 0.1)));
  });
});

describe('cellNodePosition', () => {
  const source = { x: 100, y: 200, width: 300 };
  const size = { width: 250, height: 150 };

  it('(0,0) 落在源节点右缘 + SPLIT_OFFSET_X，y 与源节点对齐', () => {
    const pos = cellNodePosition(source, 0, 0, size);
    expect(pos.x).toBe(100 + 300 + SPLIT_OFFSET_X);
    expect(pos.y).toBe(200);
  });

  it('(1,2)（row=1, col=2）按 col*(w+SPLIT_GAP) / row*(h+SPLIT_GAP) 铺开', () => {
    const pos = cellNodePosition(source, 1, 2, size);
    expect(pos.x).toBe(100 + 300 + SPLIT_OFFSET_X + 2 * (size.width + SPLIT_GAP));
    expect(pos.y).toBe(200 + 1 * (size.height + SPLIT_GAP));
  });
});
