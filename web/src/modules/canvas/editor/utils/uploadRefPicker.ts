import type { NodeType } from '../types';
import { DEFAULT_SIZES, useCanvasStore } from '../store';
import { nodeTypeForFile } from './fileDrop';
import { uploadFileIntoNode } from './uploadIntoNode';

/** 新节点摆在目标节点左侧的横向间距，与导演台「发送到画布」的 80px 对齐。 */
const GAP_X = 80;
const GAP_Y = 24;

export interface RefUploadOptions {
  /** 参考素材要连到的生成节点。 */
  targetId: string;
  /** 系统文件框的 accept 过滤器（如 'image/*'）。只是过滤器不是校验——
   *  用户仍可切「全部文件」选到任意文件，真正的准入靠 allowed。 */
  accept: string;
  /** 该生成节点接得住的输入节点类型。gen-image 只吃图，gen-video 图/音/视频都吃。 */
  allowed: NodeType[];
  /** 文件是合法媒体、但类型不被该节点接受时的拒绝理由（如「AI 绘图仅支持图片」）。 */
  rejectHint: string;
  /** 拒收回调。弹框交给调用方——这里不 import antd，保持工具函数无 UI 依赖。 */
  onRejected: (name: string, reason: string) => void;
}

/**
 * 生成节点参数面板「+」的上传入口：选文件 → 建输入节点 → **连线到目标节点** →
 * 上传。与右键菜单「上传」（ContextMenu.handleUploadClick）同一套流程，差异只有
 * 两点：按 allowed 收窄可接受类型；连线方向是 新节点 → 目标（参考图是上游）。
 *
 * 文件分类走 nodeTypeForFile、上传走 uploadFileIntoNode——都是既有唯一真源，
 * 这里不重写判定/上传逻辑（fileDrop.ts、uploadIntoNode.ts 的注释都点名过
 * 各写一套的分叉后果）。
 */
export function pickRefUploadForNode(opts: RefUploadOptions) {
  const input = document.createElement('input');
  input.type = 'file';
  input.accept = opts.accept;
  input.onchange = async (e) => {
    const file = (e.target as HTMLInputElement).files?.[0];
    if (!file) return;
    const nodeType = nodeTypeForFile(file);
    if (!nodeType) {
      opts.onRejected(file.name, file.type || '未知类型');
      return;
    }
    if (!opts.allowed.includes(nodeType)) {
      opts.onRejected(file.name, opts.rejectHint);
      return;
    }
    const store = useCanvasStore.getState();
    const target = store.nodes.find((n) => n.id === opts.targetId);
    if (!target) return;
    // 落点：目标左侧（上游方向，连线视觉上顺），已有 N 条入线就往下错开 N 档,
    // 不然连点几次「+」会叠成一摞。addNode 吃的是中心点（store 约定）。
    const size = DEFAULT_SIZES[nodeType];
    const upstreamCount = store.connections.filter((c) => c.to === opts.targetId).length;
    const newId = store.addNode(nodeType, {
      x: target.x - GAP_X - size.width / 2,
      y: target.y + size.height / 2 + upstreamCount * (size.height + GAP_Y),
    });
    if (!newId) return; // 只读画布 addNode 返回空串（store 约定）
    // addConnection 自带自环/重复检查与落库，这里不用再验。
    store.addConnection(newId, opts.targetId);
    await uploadFileIntoNode(newId, file);
  };
  input.click();
}
