// 「上一次打开的项目」记录。启动时据此直接进画布。
//
// 存 localStorage：只是一个提示，丢了也只是退回「最近修改的项目」。读写都包
// try/catch——存储不可用时当作没有记录，绝不让它挡住启动。
const KEY = 'miaoju.lastProjectId';

export function loadLastProjectId(): number | null {
  try {
    const raw = localStorage.getItem(KEY);
    const n = Number(raw);
    return raw && Number.isInteger(n) && n > 0 ? n : null;
  } catch {
    return null;
  }
}

export function saveLastProjectId(id: number): void {
  try {
    if (Number.isInteger(id) && id > 0) localStorage.setItem(KEY, String(id));
  } catch {
    /* 存储不可用：静默忽略 */
  }
}

export function clearLastProjectId(): void {
  try {
    localStorage.removeItem(KEY);
  } catch {
    /* 同上 */
  }
}
