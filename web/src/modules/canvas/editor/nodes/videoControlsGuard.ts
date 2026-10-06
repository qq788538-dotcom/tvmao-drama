import type { MouseEvent } from 'react';

// <video controls> 的原生控制条贴在元素底部，高度是浏览器固定的布局像素
// （Chrome ≈48、Safari ≈44），不随节点 resize 或画布 zoom 变。节点拖动由
// NodeShell 冒泡阶段的 onMouseDown 启动，于是视频上的 mousedown 只有两种旧写法，
// 各坏一头：全 stopPropagation → 整个画面拖不动（gen-video 曾经的样子）；
// 完全不拦 → 按进度条拖动时节点跟着跑。
//
// 折中：只把底部 56px 的控制条当交互区。判断必须换算回元素布局坐标系——clientY
// 是屏幕像素，画布 transform scale 会把控制条的屏幕高度缩放 zoom 倍，直接拿
// 屏幕像素比较在 zoom≠1 时必然失准；布局像素里控制条高度恒定，免疫缩放。
export const VIDEO_CONTROLS_DEAD_ZONE = 56;

// 「这次按下是否落在原生控制条区域」。两处消费，口径必须是同一份：
// - 视频元素自己的 onMouseDown（stopIfInVideoControls）：控制条内拦住冒泡；
// - NodeShell 的 onMouseDownCapture：只有控制条内才把 video 当交互元素做
//   「选中 + 标记早退」。画面区域若也按交互处理，冒泡阶段的拖动入口会因标记
//   早退且不 stopPropagation，事件继续冒到画布根触发**视图平移**——表现为
//   「按住视频拖，所有节点一起动」。
export function isInVideoControlsArea(el: HTMLElement, clientY: number): boolean {
  // 没有 controls 属性的纯预览视频压根没有控制条，整张脸都是画面。
  if (el instanceof HTMLVideoElement && !el.controls) return false;
  const rect = el.getBoundingClientRect();
  // 量不到尺寸（未布局/display:none 边界）就按控制条处理，宁可拖不动
  // 也别让用户按暂停键把节点拖飞。
  if (rect.height <= 0 || el.clientHeight <= 0) return true;
  const scale = rect.height / el.clientHeight;
  const localY = (clientY - rect.top) / scale;
  // 极矮节点死区最多占一半，别把整个画面都吞成不可拖（视频太矮时 Chrome
  // 本来也会隐藏控制条）。
  const dead = Math.min(VIDEO_CONTROLS_DEAD_ZONE, el.clientHeight / 2);
  return localY > el.clientHeight - dead;
}

export function stopIfInVideoControls(e: MouseEvent<HTMLElement>): void {
  if (isInVideoControlsArea(e.currentTarget, e.clientY)) e.stopPropagation();
}
