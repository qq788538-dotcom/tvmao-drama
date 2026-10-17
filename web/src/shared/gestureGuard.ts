// 全局触摸板手势守卫：拦掉浏览器对手势的两种「默认接管」——
//   1. 双指捏合缩放整个网页（保留真按着 Ctrl 滚滚轮的浏览器缩放）；
//   2. 双指左右滑触发的前进/后退导航。
//
// 都在 window 的 capture 阶段 + passive:false 处理，必须赶在浏览器执行默认
// 行为之前。preventDefault 不阻断冒泡，画布等自己处理 wheel 的组件照常收到事件。

let physicalCtrlHeld = false;

export function isPhysicalCtrlHeld(): boolean {
  return physicalCtrlHeld;
}

// canConsumeHorizontalScroll 从事件目标向上找，看有没有元素真能吃掉这次横向滚动。
//
// 为什么不能无脑 preventDefault 所有横向 wheel：页面里合法的横向滚动（宽表格、
// 横向列表、代码块）也走同一个事件，一刀切会把它们全废掉。只有当没有任何祖先
// 能消费时，浏览器才会把它转成前进/后退——那一刻才是我们该拦的。
export function canConsumeHorizontalScroll(target: EventTarget | null, deltaX: number): boolean {
  let el = target instanceof Element ? target : null;
  while (el) {
    const style = window.getComputedStyle(el);
    const overflowX = style.overflowX;
    if (overflowX === 'auto' || overflowX === 'scroll') {
      const max = el.scrollWidth - el.clientWidth;
      // 还有剩余滚动余量才算能消费：已经贴到那一侧边缘的容器会把滚动链给上层，
      // 最终仍落到浏览器导航上。
      if (max > 0) {
        if (deltaX < 0 && el.scrollLeft > 0) return true;
        if (deltaX > 0 && el.scrollLeft < max) return true;
      }
    }
    el = el.parentElement;
  }
  return false;
}

export function installGestureGuards(): void {
  window.addEventListener('keydown', (e) => {
    if (e.key === 'Control') physicalCtrlHeld = true;
  });
  window.addEventListener('keyup', (e) => {
    if (e.key === 'Control') physicalCtrlHeld = false;
  });
  // 按着 Ctrl 切走窗口再回来，keyup 会丢——失焦即重置，宁可误放行一次捏合，
  // 不能让状态卡在「以为 Ctrl 一直按着」导致捏合永远拦不住。
  window.addEventListener('blur', () => {
    physicalCtrlHeld = false;
  });

  window.addEventListener(
    'wheel',
    (e) => {
      // 捏合缩放：浏览器把它投递成 ctrlKey=true 的 wheel，与真 Ctrl+滚轮在事件
      // 字段上完全一样——唯一可靠的区分信号是键盘 Ctrl 是否真的按着。
      if (e.ctrlKey && !physicalCtrlHeld) {
        e.preventDefault();
        return;
      }
      // 横向滑动导航：只在「横向为主 + 没人能消费」时拦。纵向为主的照常放行，
      // 否则会把页面滚动一起废掉。
      if (Math.abs(e.deltaX) > Math.abs(e.deltaY) && e.deltaX !== 0) {
        if (!canConsumeHorizontalScroll(e.target, e.deltaX)) e.preventDefault();
      }
    },
    { passive: false, capture: true },
  );

  // Safari 的捏合不走 ctrl+wheel，走独立的 gesture* 事件（非标准，仅 Safari）。
  // preventDefault 只拦浏览器默认缩放，不阻断冒泡，画布仍能拿到事件自己处理。
  for (const type of ['gesturestart', 'gesturechange', 'gestureend']) {
    window.addEventListener(type, (e) => e.preventDefault(), {
      passive: false,
      capture: true,
    });
  }

  // 平板/触摸屏：禁掉触摸捏合缩放页面（pan-x pan-y = 只允许滚动）。
  // touch-action 只作用于触摸输入，不影响触摸板/鼠标的 wheel 事件。
  document.documentElement.style.touchAction = 'pan-x pan-y';
  document.body.style.touchAction = 'pan-x pan-y';

  // 声明式的第二道保险：根元素横向过卷不再转成导航。上面的 wheel 拦截靠 JS
  // 逐层判断，这条由浏览器直接兜住（含 JS 尚未接管的极早期、以及触摸屏横扫）。
  document.documentElement.style.overscrollBehaviorX = 'none';
  document.body.style.overscrollBehaviorX = 'none';
}
