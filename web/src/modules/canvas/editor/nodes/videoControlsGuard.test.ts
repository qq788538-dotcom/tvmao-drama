import { describe, expect, it, vi } from 'vitest';
import type { MouseEvent } from 'react';
import { isInVideoControlsArea, stopIfInVideoControls } from './videoControlsGuard';

// jsdom 里元素没有真实布局，直接构造带几何信息的假事件测纯逻辑。
function fakeEvent(opts: {
  clientY: number;
  clientHeight: number;
  rectTop?: number;
  rectHeight?: number;
}) {
  const stopPropagation = vi.fn();
  const e = {
    clientY: opts.clientY,
    stopPropagation,
    currentTarget: {
      clientHeight: opts.clientHeight,
      getBoundingClientRect: () => ({
        top: opts.rectTop ?? 0,
        height: opts.rectHeight ?? opts.clientHeight,
      }),
    },
  } as unknown as MouseEvent<HTMLElement>;
  return { e, stopPropagation };
}

describe('stopIfInVideoControls', () => {
  it('zoom=1：底部 56px 内拦截，其上放行', () => {
    const bottom = fakeEvent({ clientY: 390, clientHeight: 400 });
    stopIfInVideoControls(bottom.e);
    expect(bottom.stopPropagation).toHaveBeenCalled();

    const top = fakeEvent({ clientY: 100, clientHeight: 400 });
    stopIfInVideoControls(top.e);
    expect(top.stopPropagation).not.toHaveBeenCalled();
  });

  it('画布 zoom=2：按布局坐标判断，屏幕坐标写死 56px 会漏掉的位置也要拦住', () => {
    // 布局高 400，屏幕高 800。clientY=700 → 布局 y=350，落在死区(>344)；
    // 若按屏幕像素判断(700 > 800-56=744 不成立)会误放行、按进度条拖飞节点。
    const { e, stopPropagation } = fakeEvent({ clientY: 700, clientHeight: 400, rectHeight: 800 });
    stopIfInVideoControls(e);
    expect(stopPropagation).toHaveBeenCalled();
  });

  it('画布 zoom=0.5：死区随视觉缩小，布局上方区域不被误拦', () => {
    // 布局高 400，屏幕高 200。clientY=160 → 布局 y=320 < 344，应放行；
    // 屏幕像素判断(160 > 200-56=144)会误拦，死区凭空扩大。
    const { e, stopPropagation } = fakeEvent({ clientY: 160, clientHeight: 400, rectHeight: 200 });
    stopIfInVideoControls(e);
    expect(stopPropagation).not.toHaveBeenCalled();
  });

  it('极矮视频死区最多占一半', () => {
    // clientHeight=80 → 死区 min(56,40)=40。y=41(上半)放行，y=79(下半)拦截。
    const upper = fakeEvent({ clientY: 39, clientHeight: 80 });
    stopIfInVideoControls(upper.e);
    expect(upper.stopPropagation).not.toHaveBeenCalled();

    const lower = fakeEvent({ clientY: 79, clientHeight: 80 });
    stopIfInVideoControls(lower.e);
    expect(lower.stopPropagation).toHaveBeenCalled();
  });

  it('量不到尺寸时按旧行为拦截兜底', () => {
    const { e, stopPropagation } = fakeEvent({ clientY: 10, clientHeight: 0, rectHeight: 0 });
    stopIfInVideoControls(e);
    expect(stopPropagation).toHaveBeenCalled();
  });
});

describe('isInVideoControlsArea 对真实 video 元素', () => {
  function makeVideo(withControls: boolean): HTMLVideoElement {
    const v = document.createElement('video');
    if (withControls) v.setAttribute('controls', '');
    Object.defineProperty(v, 'clientHeight', { value: 400, configurable: true });
    v.getBoundingClientRect = () =>
      ({ top: 0, left: 0, right: 300, bottom: 400, width: 300, height: 400, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect;
    return v;
  }

  it('无 controls 的纯预览视频：底部也不算控制条区（整脸可拖）', () => {
    expect(isInVideoControlsArea(makeVideo(false), 399)).toBe(false);
  });

  it('有 controls：底部 56px 算控制条区', () => {
    expect(isInVideoControlsArea(makeVideo(true), 399)).toBe(true);
    expect(isInVideoControlsArea(makeVideo(true), 100)).toBe(false);
  });
});
