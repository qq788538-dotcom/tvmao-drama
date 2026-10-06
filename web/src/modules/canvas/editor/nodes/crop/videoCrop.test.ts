// 裁剪几何层的行为约定(全部分数空间,对标 liblib 实测规则):
// 角手柄锁比例、边手柄单轴、框身平移 clamp、像素换算 round-to-even + 最小框。
// 转码本体(cropVideo)依赖 WebCodecs,jsdom 跑不了,与 trim 同理不测编码路径。
import { describe, expect, it } from 'vitest';
import {
  INITIAL_RECT,
  MIN_CROP_PX,
  moveRect,
  resizeRect,
  roundEven,
  toPixelRect,
  type FracRect,
} from './videoCrop';

const base: FracRect = { left: 0.1, top: 0.1, width: 0.8, height: 0.8 };

describe('roundEven / toPixelRect', () => {
  it('四舍五入到最近偶数', () => {
    expect(roundEven(334.9)).toBe(334);
    expect(roundEven(335.1)).toBe(336);
    expect(roundEven(576)).toBe(576);
  });

  it('像素换算:尺寸偶数、位置取整(720×1280 的 80% 框 = 576×1024,对标 liblib 实测值)', () => {
    expect(toPixelRect(base, 720, 1280)).toEqual({ left: 72, top: 128, width: 576, height: 1024 });
  });

  it('round 顶出边界时 left/top 被拉回,right/bottom 不越界', () => {
    // 宽度 round 后可能比 (1-left)*videoW 大 1~2px,left 要回退
    const r: FracRect = { left: 0.5, top: 0.5, width: 0.5, height: 0.5 };
    const px = toPixelRect(r, 719, 719);
    expect(px.left + px.width).toBeLessThanOrEqual(719);
    expect(px.top + px.height).toBeLessThanOrEqual(719);
    expect(px.width % 2).toBe(0);
    expect(px.height % 2).toBe(0);
  });

  it('小于最小框时抬到 MIN_CROP_PX', () => {
    const px = toPixelRect({ left: 0, top: 0, width: 0.01, height: 0.01 }, 720, 1280);
    expect(px.width).toBe(MIN_CROP_PX);
    expect(px.height).toBe(MIN_CROP_PX);
  });
});

describe('moveRect', () => {
  it('平移不改尺寸', () => {
    const r = moveRect(base, 0.05, -0.03);
    expect(r.left).toBeCloseTo(0.15);
    expect(r.top).toBeCloseTo(0.07);
    expect(r.width).toBe(0.8);
    expect(r.height).toBe(0.8);
  });

  it('clamp 在画面内:拖出边界停在边上', () => {
    expect(moveRect(base, 1, 1)).toEqual({ ...base, left: 0.19999999999999996, top: 0.19999999999999996 });
    expect(moveRect(base, -1, -1)).toEqual({ ...base, left: 0, top: 0 });
  });
});

describe('resizeRect 边手柄(单轴自由)', () => {
  it('e:只改宽度,高度/位置不动', () => {
    const r = resizeRect(base, 'e', -0.2, 0.5, 0.05, 0.05);
    expect(r.width).toBeCloseTo(0.6);
    expect(r.height).toBe(0.8);
    expect(r.left).toBe(0.1);
    expect(r.top).toBe(0.1);
  });

  it('w:左边动,右边(left+width)不动', () => {
    const r = resizeRect(base, 'w', 0.2, 0, 0.05, 0.05);
    expect(r.left).toBeCloseTo(0.3);
    expect(r.left + r.width).toBeCloseTo(0.9);
  });

  it('n/s:只改纵轴', () => {
    const n = resizeRect(base, 'n', 0.5, 0.1, 0.05, 0.05);
    expect(n.top).toBeCloseTo(0.2);
    expect(n.top + n.height).toBeCloseTo(0.9);
    expect(n.left).toBe(0.1);
    const s = resizeRect(base, 's', 0, -0.1, 0.05, 0.05);
    expect(s.height).toBeCloseTo(0.7);
    expect(s.top).toBe(0.1);
  });

  it('缩到最小值停住,不翻转', () => {
    const r = resizeRect(base, 'e', -5, 0, 0.05, 0.05);
    expect(r.width).toBeCloseTo(0.05);
  });
});

describe('resizeRect 角手柄(锁比例,锚对角)', () => {
  it('se:宽高同比例缩放(liblib 实测:80%→61% 两轴同步)', () => {
    const r = resizeRect(base, 'se', -0.2, -0.1, 0.01, 0.01);
    // 主导轴是 dx(-0.2):s = 0.6/0.8 = 0.75
    expect(r.width).toBeCloseTo(0.6);
    expect(r.height).toBeCloseTo(0.6);
    // 锚点 = 左上角不动
    expect(r.left).toBe(0.1);
    expect(r.top).toBe(0.1);
  });

  it('nw:锚右下角,left/top 跟着缩', () => {
    const r = resizeRect(base, 'nw', 0.4, 0.1, 0.01, 0.01);
    // 主导轴 dx(0.4) 向内:s = 0.4/0.8 = 0.5
    expect(r.width).toBeCloseTo(0.4);
    expect(r.height).toBeCloseTo(0.4);
    expect(r.left + r.width).toBeCloseTo(0.9);
    expect(r.top + r.height).toBeCloseTo(0.9);
  });

  it('放大越界时被可用空间 clamp(比例仍锁)', () => {
    // se 向外拖很多:左上锚点在 (0.1,0.1),可用空间 s ≤ min(0.9/0.8, 0.9/0.8)
    const r = resizeRect(base, 'se', 5, 5, 0.01, 0.01);
    expect(r.width).toBeCloseTo(0.9);
    expect(r.height).toBeCloseTo(0.9);
    expect(r.width / r.height).toBeCloseTo(base.width / base.height);
  });

  it('缩小到最小框停住', () => {
    const r = resizeRect(base, 'se', -5, -5, 0.1, 0.05);
    // s 下限 = max(minW/w, minH/h) = max(0.125, 0.0625) = 0.125
    expect(r.width).toBeCloseTo(0.1);
    expect(r.height).toBeCloseTo(0.1);
  });
});

describe('INITIAL_RECT', () => {
  it('居中 80%(对标 liblib 初始框)', () => {
    expect(INITIAL_RECT).toEqual({ left: 0.1, top: 0.1, width: 0.8, height: 0.8 });
  });
});
