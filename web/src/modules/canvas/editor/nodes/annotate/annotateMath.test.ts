// 标注坐标数学的回归：坐标空间约定（一律原图像素）是整个功能的地基——
// fitRect/toNatural 错了，标注会画在图外或错位；naturalStrokeWidth 的
// zoom 不变性破了，同一档线宽在不同缩放下导出粗细不同。
import { describe, expect, it, vi } from 'vitest';
import {
  commitTextDraft,
  fitRect,
  naturalStrokeWidth,
  replayStrokes,
  toNatural,
  type AnnotateStroke,
} from './annotateMath';

describe('fitRect', () => {
  it('图与盒同宽高比：铺满、无 letterbox', () => {
    const f = fitRect(1600, 800, 400, 200);
    expect(f).toMatchObject({ x: 0, y: 0, width: 400, height: 200 });
    expect(f.scale).toBe(4);
  });

  it('宽图进方盒：上下留边、水平铺满', () => {
    const f = fitRect(1000, 500, 200, 200);
    expect(f).toMatchObject({ x: 0, y: 50, width: 200, height: 100 });
    expect(f.scale).toBe(5);
  });

  it('高图进方盒：左右留边', () => {
    const f = fitRect(500, 1000, 200, 200);
    expect(f).toMatchObject({ x: 50, y: 0, width: 100, height: 200 });
  });
});

describe('toNatural', () => {
  const fit = fitRect(1000, 500, 200, 200); // 上下各留 50，scale=5
  it('图内点：减 letterbox 偏移后按 scale 放大', () => {
    expect(toNatural(100, 100, fit, 1000, 500)).toEqual({ x: 500, y: 250 });
  });
  it('越界点钳到图内', () => {
    expect(toNatural(-10, 0, fit, 1000, 500)).toEqual({ x: 0, y: 0 });
    expect(toNatural(999, 999, fit, 1000, 500)).toEqual({ x: 1000, y: 500 });
  });
});

describe('naturalStrokeWidth', () => {
  it('zoom 不变性：同一滑杆档位在不同 zoom 下换算出同一原图线宽', () => {
    // 图片世界宽 500、原图宽 1000。zoom=1 时显示宽 500 → fitScale=2；
    // zoom=2 时显示宽 1000 → fitScale=1。乘积恒等。
    expect(naturalStrokeWidth(4, 2, 1)).toBe(8);
    expect(naturalStrokeWidth(4, 1, 2)).toBe(8);
  });
});

describe('commitTextDraft', () => {
  const draft = { x: 10, y: 20, text: 'fix', color: '#fff', fontSize: 24 };
  it('非空文字追加为 text 笔画', () => {
    const next = commitTextDraft([], draft);
    expect(next).toEqual([{ kind: 'text', color: '#fff', fontSize: 24, x: 10, y: 20, text: 'fix' }]);
  });
  it('空白文字返回原数组（=== 可判没变）', () => {
    const prev: AnnotateStroke[] = [];
    expect(commitTextDraft(prev, { ...draft, text: '  ' })).toBe(prev);
  });
});

describe('replayStrokes', () => {
  // jsdom 没有真 canvas，mock 一个记录调用的 ctx——测的是「每种笔画驱动了
  // 哪些绘制调用」，像素级验收靠实测。
  function mockCtx() {
    return {
      strokeStyle: '',
      fillStyle: '',
      lineWidth: 0,
      lineCap: '',
      lineJoin: '',
      font: '',
      textBaseline: '',
      beginPath: vi.fn(),
      moveTo: vi.fn(),
      lineTo: vi.fn(),
      stroke: vi.fn(),
      strokeRect: vi.fn(),
      fillText: vi.fn(),
    } as unknown as CanvasRenderingContext2D & Record<string, ReturnType<typeof vi.fn>>;
  }

  it('pen：折线 stroke；单点也画（零长线靠 round cap 成点）', () => {
    const ctx = mockCtx();
    replayStrokes(ctx, [
      { kind: 'pen', color: '#f00', width: 6, points: [{ x: 1, y: 2 }] },
      { kind: 'pen', color: '#f00', width: 6, points: [{ x: 1, y: 2 }, { x: 3, y: 4 }] },
    ]);
    expect(ctx.stroke).toHaveBeenCalledTimes(2);
    // 单点笔画补了一段回到原点的零长线。
    expect(ctx.lineTo).toHaveBeenCalledWith(1, 2);
  });

  it('rect：空心描边矩形', () => {
    const ctx = mockCtx();
    replayStrokes(ctx, [{ kind: 'rect', color: '#f00', width: 3, x: 5, y: 6, w: 40, h: 30 }]);
    expect(ctx.strokeRect).toHaveBeenCalledWith(5, 6, 40, 30);
    expect(ctx.lineWidth).toBe(3);
  });

  it('text：按行 fillText，行高 1.25 倍字号', () => {
    const ctx = mockCtx();
    replayStrokes(ctx, [{ kind: 'text', color: '#fff', fontSize: 20, x: 10, y: 20, text: 'a\nb' }]);
    expect(ctx.fillText).toHaveBeenNthCalledWith(1, 'a', 10, 20);
    expect(ctx.fillText).toHaveBeenNthCalledWith(2, 'b', 10, 45);
  });
});
