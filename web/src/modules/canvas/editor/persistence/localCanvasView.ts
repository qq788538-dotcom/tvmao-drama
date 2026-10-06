// 画布/故事板视图偏好的本地记忆。
//
// 跟同目录的 localView.ts（画布 pan/zoom）刻意不同：那个**按项目**存，换个
// 项目就该有自己的视角；这个是**全局一个值**，所有画布共用——用户选的是
// "我习惯用哪种方式看项目"，不是"这个项目该怎么看"。在项目 A 切到故事板，
// 进项目 B 也该直接是故事板。
export type CanvasViewMode = 'canvas' | 'storyboard';

const KEY = 'tvmao:canvas:viewMode';
const DEFAULT_MODE: CanvasViewMode = 'canvas';

/**
 * 读取上次选的视图。没记录、值非法、或 localStorage 不可用时都回落到画布。
 *
 * 必须在 useState 的初始化函数里调用，不能放 useEffect 事后再 setView：
 * 上次选的是故事板时，事后切会先渲染一帧画布再跳走——用户看到闪一下，
 * 而且那一帧的画布是真的挂载了一次。
 */
export function loadCanvasViewMode(): CanvasViewMode {
  try {
    const raw = window.localStorage.getItem(KEY);
    // localStorage 里的东西不可信：用户手改过、或以后视图种类增删过，
    // 都可能读出一个当前不存在的模式。不校验就会渲染出一个不存在的视图。
    return raw === 'canvas' || raw === 'storyboard' ? raw : DEFAULT_MODE;
  } catch {
    // 隐私模式 / 被禁用 / iframe 跨域都会抛。存个偏好而已，不该把画布页搞崩。
    return DEFAULT_MODE;
  }
}

export function saveCanvasViewMode(mode: CanvasViewMode): void {
  try {
    window.localStorage.setItem(KEY, mode);
  } catch {
    // 同上：写失败就当没记住，下次进来用默认值，不影响本次使用。
  }
}
