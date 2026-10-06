// 旋转纯计算的约定钉死在这里：rotatedBox 在 90 倍数角度必须精确退化成宽高
// 互换/不变（这个尺寸就是烘焙画布的像素尺寸，90° 转完差 1px 都会带出半透明
// 边）；rotatedNodeSize 必须与 InputImageNode 的 auto-fit 公式逐字一致
// （targetArea 290²、下限 200），否则结果节点在上传完成瞬间跳尺寸。
import { describe, expect, it } from 'vitest';
import { clampAngle, rotatedBox, rotatedNodeSize } from './rotateMath';

describe('clampAngle', () => {
  it('钳到 [0,360] 整数，非法输入落 0', () => {
    expect(clampAngle(90)).toBe(90);
    expect(clampAngle(90.6)).toBe(91);
    expect(clampAngle(-10)).toBe(0);
    expect(clampAngle(400)).toBe(360);
    expect(clampAngle(NaN)).toBe(0);
    expect(clampAngle(Infinity)).toBe(0);
  });
});

describe('rotatedBox', () => {
  it('90 倍数角度精确退化：0/180 不变，90/270 宽高互换', () => {
    expect(rotatedBox(1548, 1016, 0)).toEqual({ width: 1548, height: 1016 });
    expect(rotatedBox(1548, 1016, 180)).toEqual({ width: 1548, height: 1016 });
    expect(rotatedBox(1548, 1016, 90)).toEqual({ width: 1016, height: 1548 });
    expect(rotatedBox(1548, 1016, 270)).toEqual({ width: 1016, height: 1548 });
    expect(rotatedBox(1548, 1016, 360)).toEqual({ width: 1548, height: 1016 });
  });

  it('45°：正方形外接矩形边长 = 边 × √2', () => {
    const box = rotatedBox(100, 100, 45);
    expect(box.width).toBe(141);
    expect(box.height).toBe(141);
  });

  it('任意角度：外接矩形不小于原图任一投影，且 θ 与 180+θ 同尺寸', () => {
    const a = rotatedBox(1548, 1016, 30);
    const b = rotatedBox(1548, 1016, 210);
    expect(a).toEqual(b);
    expect(a.width).toBeGreaterThanOrEqual(1548);
    expect(a.height).toBeGreaterThanOrEqual(1016);
  });
});

describe('rotatedNodeSize', () => {
  it('与 InputImageNode auto-fit 同公式：面积≈290²，窄边钳 200', () => {
    const s = rotatedNodeSize(1548 / 1016);
    expect(s.width * s.height).toBeGreaterThan(290 * 290 * 0.98);
    expect(s.width * s.height).toBeLessThan(290 * 290 * 1.02);
    // 极端宽图：高被钳到下限。
    expect(rotatedNodeSize(10).height).toBe(200);
  });
});
