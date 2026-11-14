import { useCanvasStore } from '../store';
import type { ViewState } from '../types';
import { useStoryboardMode } from '../../storyboard/StoryboardContext';

// 画布节点上的视频是否该挂载媒体源（P15/P25，方案 2.2）。
//
// 规则：节点矩形与「视口四周各扩一屏」相交才活跃——提前一屏挂上，滚到眼前时
// 元数据/首帧已经就绪。剪辑(trim)、裁剪(crop)、片段重拍、智能续写期间强制活跃：
// 裁剪条/裁剪层直接驱动宿主节点里的 <video>（见 VideoTrimBar、VideoCropOverlay），
// 源被卸掉就量不到尺寸、预览不了选区。正在播放、全屏由 VideoPlayer 自己兜住。
// 故事板详情面板不在画布坐标系里，恒为活跃。
//
// 视口尺寸用「窗口减侧栏」近似，与 centerNode / Minimap 同一套算法。

export interface NodeRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** 节点矩形是否落在视口（四周各加一屏边距）内。screen = world*zoom + view.x。 */
export function isNodeNearViewport(rect: NodeRect, view: ViewState, vpW: number, vpH: number): boolean {
  const zoom = view.zoom > 0 ? view.zoom : 1;
  const left = (-view.x - vpW) / zoom;
  const right = (-view.x + 2 * vpW) / zoom;
  const top = (-view.y - vpH) / zoom;
  const bottom = (-view.y + 2 * vpH) / zoom;
  return rect.x < right && rect.x + rect.width > left && rect.y < bottom && rect.y + rect.height > top;
}

export function useNodeMediaActive(nodeId: string): boolean {
  const storyboardMode = useStoryboardMode();
  // selector 只返回布尔：平移/缩放的每一帧都会跑，但只有跨过边界时才触发重渲染。
  const near = useCanvasStore((s) => {
    if (
      s.trimmingNodeId === nodeId ||
      s.croppingNodeId === nodeId ||
      s.reshootNodeId === nodeId ||
      s.continuationNodeId === nodeId
    ) {
      return true;
    }
    const node = s.nodesById.get(nodeId);
    if (!node) return true;
    return isNodeNearViewport(node, s.view, window.innerWidth - 56, window.innerHeight - 56);
  });
  return storyboardMode || near;
}
