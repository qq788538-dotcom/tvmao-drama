import { useCanvasStore } from '../../store';
import { persistNodeById } from '../../persistence/canvasMutations';

// 资产 ↔ 画布节点的桥。设计取舍：脚本节点的 ScriptAsset 自己不存图，图的本体
// 永远是一个真实画布节点（input-image / gen-image），资产只存一根 linkedNodeId
// 指针连过去——同一张图可能被素材库/其它节点复用，图片节点是唯一真源。
//
// 全部结构性操作（新建节点、连线）经 store.addNode/addConnection：这两个 action
// 自己会 emitCanvasOp 广播 upsertNode/upsertConnection，被 canvasMutations.ts 的
// applyCanvasOp 立即落库，本文件不用逐个显式 persist。唯一例外是
// createGenAssetNode 里紧跟着的 updateNodeSettings 写 prompt——updateNodeSettings
// 本身不广播 op（它也被生成回写复用，见 store.ts 顶部注释），必须补一次
// persistNodeById，否则刷新页面 prompt 就丢了。

const ASSET_NODE_X_OFFSET = 360; // 距脚本节点左缘/列间的水平步长
const ASSET_NODE_Y_STEP = 240; // 同列资产节点纵向错开的步长，避免互相叠住
const ASSET_NODE_COL_SIZE = 4; // 每列几个。单列纵排时第 20 个资产会落到 4800px 外，
// 用户得拉半天画布才找到刚生成的图（线上反馈）——网格让所有资产都聚在脚本节点左邻。

// 算新节点的落位点：脚本节点左侧、每列 ASSET_NODE_COL_SIZE 个、列向左扩展的网格。
// addNode 把传入坐标当**中心点**（见 store.ts addNode 内部 `x - size.width / 2`），
// 这里直接返回目标中心点，不用再手动减半宽。
function assetNodeAnchor(scriptNodeId: string, index: number): { x: number; y: number } | null {
  const script = useCanvasStore.getState().nodes.find((n) => n.id === scriptNodeId);
  if (!script) return null;
  const col = Math.floor(index / ASSET_NODE_COL_SIZE);
  const row = index % ASSET_NODE_COL_SIZE;
  return {
    x: script.x - ASSET_NODE_X_OFFSET * (col + 1),
    y: script.y + row * ASSET_NODE_Y_STEP,
  };
}

// 建一个待用户手动上传图片的 input-image 节点，连线指向脚本节点，返回新节点 id。
// 脚本节点找不到（已被删）时返回空字符串——调用方（AssetsPanel）据此不写回 linkedNodeId。
export function createUploadAssetNode(scriptNodeId: string, index: number): string {
  const anchor = assetNodeAnchor(scriptNodeId, index);
  if (!anchor) return '';
  const { addNode, addConnection } = useCanvasStore.getState();
  const id = addNode('input-image', anchor);
  if (!id) return '';
  addConnection(id, scriptNodeId);
  return id;
}

// 建一个 gen-image 节点并预填 prompt，连线指向脚本节点，返回新节点 id。
// 不自动提交生成——模型选择/参数/积分校验/提交这一整套已经是 gen-image 节点自己的
// 职责，面板这里重造一遍纯属重复建设，交给用户在节点上自己点「生成」。
export function createGenAssetNode(
  scriptNodeId: string,
  index: number,
  prompt: string,
  extraSettings?: Record<string, unknown>,
): string {
  const anchor = assetNodeAnchor(scriptNodeId, index);
  if (!anchor) return '';
  const { addNode, addConnection, updateNodeSettings } = useCanvasStore.getState();
  const id = addNode('gen-image', anchor);
  if (!id) return '';
  addConnection(id, scriptNodeId);
  updateNodeSettings(id, { prompt, ...extraSettings });
  persistNodeById(id);
  return id;
}

// 绑定画布上已有的图片节点为资产图：只连线不建节点（同一张图可被多处复用，
// 图片节点是唯一真源）。重复连线由 store.addConnection 自身幂等去重。
export function linkExistingAssetNode(scriptNodeId: string, imageNodeId: string): boolean {
  const store = useCanvasStore.getState();
  if (!store.nodes.some((n) => n.id === imageNodeId)) return false;
  store.addConnection(imageNodeId, scriptNodeId);
  return true;
}

// 资产绑定的画布节点是否还活着——linkedNodeId 指向的节点随时可能被用户从画布上
// 直接删掉，卡片渲染前得先判一下，不然会拿着死指针去读 content 渲染出空白/崩溃。
export function assetLinkedNodeAlive(nodeId: string): boolean {
  return useCanvasStore.getState().nodes.some((n) => n.id === nodeId);
}

// 资产与图片节点解绑时的连线清理：只有当**没有其他资产**还引用同一图片节点时，
// 才移除「图片节点 → 脚本节点」这条边（同一张图可被多个资产复用——从画布选择
// 的绑定就是共享引用）。图片节点本体永远不删：它可能还被画布上别处使用。
export function detachAssetNodeLink(scriptNodeId: string, imageNodeId: string, stillReferenced: boolean): void {
  if (!imageNodeId || stillReferenced) return;
  const store = useCanvasStore.getState();
  const edge = store.connections.find((c) => c.from === imageNodeId && c.to === scriptNodeId);
  if (edge) store.removeConnection(edge.id);
}
