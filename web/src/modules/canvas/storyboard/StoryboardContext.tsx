// 故事板模式开关，用 context 下发而不是加 prop——14 个节点组件的签名都是
// `{ id: string }`，加 prop 意味着 14 个文件挨个改一遍再逐个透传给 NodeShell。
// context 让节点组件一个都不用动。
import { createContext, useContext } from 'react';

export const StoryboardModeContext = createContext(false);

/** 当前节点是否渲染在故事板里（而不是画布上）。 */
export function useStoryboardMode(): boolean {
  return useContext(StoryboardModeContext);
}
