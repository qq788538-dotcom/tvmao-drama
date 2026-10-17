import { useCanvasStore } from '../store';
import { nodeTypeForFile } from './fileDrop';
import { uploadFileIntoNode } from './uploadIntoNode';

export interface UploadPickerOptions {
  /** 新节点的世界坐标落点（中心点，addNode 按中心放置）。 */
  worldX: number;
  worldY: number;
  /** 设置后，新节点会自动从这个 source 节点连过来（拖连线松手的场景）。 */
  sourceNodeId?: string;
  /** 拒收回调。弹框交给调用方——这里不 import antd，保持工具函数无 UI 依赖
   *  （与 uploadRefPicker 的 onRejected 同一约定）。 */
  onRejected: (name: string, reason: string) => void;
}

/**
 * 「上传」菜单项的共用入口：选文件 → 按 MIME 建输入节点 → 上传。
 * 添加节点菜单（ContextMenu）和右键菜单（CanvasContextMenu）都走这里——
 * 文件分类走 nodeTypeForFile、上传走 uploadFileIntoNode，两个唯一真源的
 * 注释都点名过各写一套的分叉后果，菜单层不重写任何判定。
 */
export function openUploadPicker(opts: UploadPickerOptions) {
  const input = document.createElement('input');
  input.type = 'file';
  input.accept = 'image/*,audio/*,video/*';
  input.onchange = async (e) => {
    const file = (e.target as HTMLInputElement).files?.[0];
    if (!file) return;
    const nodeType = nodeTypeForFile(file);
    if (!nodeType) {
      // accept 属性只是过滤器，用户在系统文件框里仍可选到任意文件。
      opts.onRejected(file.name, file.type || '未知类型');
      return;
    }
    const { addNode, addConnection } = useCanvasStore.getState();
    const newNodeId = addNode(nodeType, { x: opts.worldX, y: opts.worldY });
    if (!newNodeId) return; // 只读模式下 addNode 返回空串
    if (opts.sourceNodeId) addConnection(opts.sourceNodeId, newNodeId);
    await uploadFileIntoNode(newNodeId, file);
  };
  input.click();
}
