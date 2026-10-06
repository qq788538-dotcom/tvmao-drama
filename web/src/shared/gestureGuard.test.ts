import { beforeEach, describe, expect, it } from 'vitest';
import { canConsumeHorizontalScroll, installGestureGuards } from './gestureGuard';

// 造一个「能横向滚动」的元素:jsdom 不做布局,scrollWidth/clientWidth 恒为 0,
// 必须自己定义这几个几何属性,否则测的是 jsdom 的空布局而不是我们的判定逻辑。
function scrollableX(opts: { scrollWidth: number; clientWidth: number; scrollLeft: number }) {
  const el = document.createElement('div');
  el.style.overflowX = 'auto';
  Object.defineProperty(el, 'scrollWidth', { value: opts.scrollWidth });
  Object.defineProperty(el, 'clientWidth', { value: opts.clientWidth });
  Object.defineProperty(el, 'scrollLeft', { value: opts.scrollLeft, writable: true });
  return el;
}

describe('canConsumeHorizontalScroll', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });

  it('页面里没有横向滚动容器时判定为无人消费(该拦)', () => {
    const plain = document.createElement('div');
    document.body.appendChild(plain);

    expect(canConsumeHorizontalScroll(plain, 40)).toBe(false);
    expect(canConsumeHorizontalScroll(plain, -40)).toBe(false);
  });

  it('祖先容器还有余量时判定为可消费(该放行)——宽表格/横向列表不被误伤', () => {
    const box = scrollableX({ scrollWidth: 1000, clientWidth: 300, scrollLeft: 100 });
    const child = document.createElement('span');
    box.appendChild(child);
    document.body.appendChild(box);

    expect(canConsumeHorizontalScroll(child, 40)).toBe(true); // 右滑,右边还有 600 余量
    expect(canConsumeHorizontalScroll(child, -40)).toBe(true); // 左滑,左边还有 100 余量
  });

  it('容器已贴到边缘时判定为无人消费——滚动会链到浏览器变成导航', () => {
    const atLeftEdge = scrollableX({ scrollWidth: 1000, clientWidth: 300, scrollLeft: 0 });
    document.body.appendChild(atLeftEdge);
    expect(canConsumeHorizontalScroll(atLeftEdge, -40)).toBe(false); // 已在最左还想左滑
    expect(canConsumeHorizontalScroll(atLeftEdge, 40)).toBe(true); // 往右仍有余量

    const atRightEdge = scrollableX({ scrollWidth: 1000, clientWidth: 300, scrollLeft: 700 });
    document.body.appendChild(atRightEdge);
    expect(canConsumeHorizontalScroll(atRightEdge, 40)).toBe(false); // 已在最右还想右滑
  });

  it('内容没有溢出的容器不算能消费', () => {
    const noOverflow = scrollableX({ scrollWidth: 300, clientWidth: 300, scrollLeft: 0 });
    document.body.appendChild(noOverflow);

    expect(canConsumeHorizontalScroll(noOverflow, 40)).toBe(false);
  });
});

describe('installGestureGuards 的 wheel 拦截', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
    installGestureGuards();
  });

  function fireWheel(target: Element, init: WheelEventInit) {
    const e = new WheelEvent('wheel', { bubbles: true, cancelable: true, ...init });
    target.dispatchEvent(e);
    return e;
  }

  it('横向为主且无人消费 → 拦掉(不让浏览器前进/后退)', () => {
    const plain = document.createElement('div');
    document.body.appendChild(plain);

    expect(fireWheel(plain, { deltaX: -60, deltaY: 2 }).defaultPrevented).toBe(true);
  });

  it('纵向为主 → 放行(页面滚动不能被废掉)', () => {
    const plain = document.createElement('div');
    document.body.appendChild(plain);

    expect(fireWheel(plain, { deltaX: 3, deltaY: 80 }).defaultPrevented).toBe(false);
  });

  it('横向为主但容器能消费 → 放行', () => {
    const box = scrollableX({ scrollWidth: 1000, clientWidth: 300, scrollLeft: 100 });
    document.body.appendChild(box);

    expect(fireWheel(box, { deltaX: 60, deltaY: 0 }).defaultPrevented).toBe(false);
  });

  it('捏合(ctrlKey 但物理 Ctrl 没按) → 仍然拦掉', () => {
    const plain = document.createElement('div');
    document.body.appendChild(plain);

    expect(fireWheel(plain, { deltaY: -10, ctrlKey: true }).defaultPrevented).toBe(true);
  });
});
