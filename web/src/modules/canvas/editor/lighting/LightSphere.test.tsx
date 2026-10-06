import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { LIGHT_DIRECTIONS, snapToDirection } from './directions';
import { buildLightPresetParams, hasLightDirtyState } from './lightParams';
import { directionToWorld, projectPoint, rayToSphere } from './projection';
import { LightSphere } from './LightSphere';

describe('directions', () => {
  it('26 档枚举完整且 key 唯一', () => {
    expect(LIGHT_DIRECTIONS).toHaveLength(26);
    expect(new Set(LIGHT_DIRECTIONS.map((d) => d.key)).size).toBe(26);
  });

  it('每档的标称角度吸附回自身（吸附函数与档位表自洽）', () => {
    for (const d of LIGHT_DIRECTIONS) {
      expect(snapToDirection(d.yaw, d.pitch)).toBe(d.key);
    }
  });

  it('偏离标称角度落到最近档', () => {
    expect(snapToDirection(10, 5)).toBe('front');
    expect(snapToDirection(-40, 30)).toBe('high-front-left'); // 315° 与 -45° 同向
    expect(snapToDirection(180, -85)).toBe('bottom');
    expect(snapToDirection(0, 80)).toBe('top');
  });
});

describe('projection（liblib 实测相机）', () => {
  it('投影↔射线求交往返一致：可见半球方向投影后按像素反求回同一方向', () => {
    // 透视相机在 (11,8,10)（方位约 48°），取其可见侧方向；正面相机取前半球。
    const cases: Array<['persp' | 'front', number, number]> = [
      ['persp', 0, 0],
      ['persp', 45, 0],
      ['persp', 45, 35],
      // 注意用例必须选相机可见（法向朝相机）的方向：被球体遮挡的点其像素射线
      // 命中的是近侧表面，往返必然对不上——那是几何事实不是 bug。
      ['persp', 90, 35],
      ['front', 0, 0],
      ['front', -45, 35],
      ['front', 45, -35],
    ];
    for (const [view, yaw, pitch] of cases) {
      const p = projectPoint(view, directionToWorld(yaw, pitch));
      const back = rayToSphere(view, p.x, p.y);
      expect(Math.abs(back.yaw - yaw)).toBeLessThan(1.5);
      expect(Math.abs(back.pitch - pitch)).toBeLessThan(1.5);
    }
  });

  it('光标在球轮廓外时贴轮廓返回合法方向（不抛错不出 NaN）', () => {
    const r = rayToSphere('persp', 0, 0);
    expect(Number.isFinite(r.yaw)).toBe(true);
    expect(Number.isFinite(r.pitch)).toBe(true);
  });
});

describe('buildLightPresetParams（脏标记语义）', () => {
  const base = {
    direction: 'front',
    sceneTouched: false,
    rimEnabled: false,
    brightness: 50,
    brightnessTouched: false,
    lightColor: '#ffffff',
    colorTouched: false,
    presetKey: '',
    hasReferenceImage: false,
  };

  it('全默认 → 空参数（没碰过的组一律不发）', () => {
    expect(buildLightPresetParams(base)).toEqual({});
    expect(hasLightDirtyState(base, '')).toBe(false);
    expect(hasLightDirtyState(base, '  ')).toBe(false);
  });

  it('碰过光位组 → keyLight；开轮廓光附带 rimLight=back', () => {
    expect(buildLightPresetParams({ ...base, sceneTouched: true, direction: 'high-front-left' })).toEqual({
      keyLight: 'high-front-left',
    });
    expect(
      buildLightPresetParams({ ...base, sceneTouched: true, direction: 'left', rimEnabled: true }),
    ).toEqual({ keyLight: 'left', rimLight: 'back' });
  });

  it('主光源在正后方时不发轮廓光（liblib 同款跳过）', () => {
    expect(
      buildLightPresetParams({ ...base, sceneTouched: true, direction: 'back', rimEnabled: true }),
    ).toEqual({ keyLight: 'back' });
  });

  it('亮度/颜色/预设/参考图各自独立进参数', () => {
    expect(
      buildLightPresetParams({
        ...base,
        brightnessTouched: true,
        brightness: 80,
        colorTouched: true,
        lightColor: '#2d34fa',
        presetKey: 'rembrandt',
        hasReferenceImage: true,
      }),
    ).toEqual({ brightness: '80', lightColor: '#2d34fa', preset: 'rembrandt', hasRef: '1' });
  });
});

describe('LightSphere', () => {
  // jsdom 的 fireEvent.pointerDown 造不出 clientX/pointerId（全 undefined），
  // 改用真实 MouseEvent 实例派发 pointer 事件（自带 clientX；pointerId 两侧
  // 同为 undefined，配对判定不受影响）。
  const pointer = (el: Element, type: string, x: number, y: number) => {
    fireEvent(el, new MouseEvent(type, { bubbles: true, clientX: x, clientY: y }));
  };

  it('渲染不炸：光点/光锥存在', () => {
    render(<LightSphere direction="front" view="persp" onChange={() => {}} />);
    expect(screen.getByTestId('light-dot')).toBeTruthy();
    expect(screen.getByTestId('light-cone')).toBeTruthy();
  });

  it('从光点拖到目标像素：回调值 = 该像素射线求交的吸附档（正面视角）', () => {
    const onChange = vi.fn();
    render(<LightSphere direction="front" view="front" onChange={onChange} />);
    const el = screen.getByTestId('light-sphere');
    const start = projectPoint('front', directionToWorld(0, 0));
    pointer(el, 'pointerdown', start.x, start.y);
    pointer(el, 'pointermove', 160, 100);
    pointer(el, 'pointerup', 160, 100);
    const want = snapToDirection(rayToSphere('front', 160, 100).yaw, rayToSphere('front', 160, 100).pitch);
    expect(onChange).toHaveBeenLastCalledWith(want);
    expect(want).not.toBe('front'); // 160px 处必然已离开 front 档
  });

  it('透视视角同样走射线求交（从 front 档光点拖到球心方向）', () => {
    const onChange = vi.fn();
    render(<LightSphere direction="front" view="persp" onChange={onChange} />);
    const el = screen.getByTestId('light-sphere');
    const start = projectPoint('persp', directionToWorld(0, 0));
    pointer(el, 'pointerdown', start.x, start.y);
    pointer(el, 'pointermove', 100, 100);
    pointer(el, 'pointerup', 100, 100);
    const r = rayToSphere('persp', 100, 100);
    expect(onChange).toHaveBeenLastCalledWith(snapToDirection(r.yaw, r.pitch));
  });

  it('按下位置不在光点附近则不进入拖拽（liblib 同款：随手点球面不改光位）', () => {
    const onChange = vi.fn();
    render(<LightSphere direction="front" view="front" onChange={onChange} />);
    const el = screen.getByTestId('light-sphere');
    pointer(el, 'pointerdown', 20, 20);
    pointer(el, 'pointermove', 90, 90);
    pointer(el, 'pointerup', 90, 90);
    expect(onChange).not.toHaveBeenCalled();
  });
});

describe('亮度联动', () => {
  const coneSpread = (points: string | null) => {
    const ys = (points ?? '')
      .split(' ')
      .map((p) => Number(p.split(',')[1]))
      .filter((n) => Number.isFinite(n));
    return Math.max(...ys) - Math.min(...ys);
  };

  it('亮度越高锥体越宽（0 档细针，100 档宽于 50 档）', () => {
    const spreadAt = (b: number) => {
      const { unmount, getByTestId } = render(
        <LightSphere direction="front" view="persp" brightness={b} onChange={() => {}} />,
      );
      const s = coneSpread(getByTestId('light-cone').getAttribute('points'));
      unmount();
      return s;
    };
    const s0 = spreadAt(0);
    const s50 = spreadAt(50);
    const s100 = spreadAt(100);
    // spread 含锥尖到锥口的整体对角跨度，细针也有基础值——只断言单调与显著加宽。
    expect(s0).toBeLessThan(s50);
    expect(s50).toBeLessThan(s100);
    expect(s100).toBeGreaterThan(s0 + 20);
  });

  it('缺省亮度按 50 处理（与显式 50 同宽）', () => {
    const r1 = render(<LightSphere direction="front" view="persp" onChange={() => {}} />);
    const a = r1.getByTestId('light-cone').getAttribute('points');
    r1.unmount();
    const r2 = render(
      <LightSphere direction="front" view="persp" brightness={50} onChange={() => {}} />,
    );
    const b = r2.getByTestId('light-cone').getAttribute('points');
    expect(a).toBe(b);
  });
});
