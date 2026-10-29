import { useRef } from 'react';
import { create, type StateCreator } from 'zustand';
import { nanoid } from 'nanoid';
import { setApiWritesBlocked } from '@/shared/apiGuards';
import { remapMentionNodeIds } from './utils/mentions';
import { cloneWithoutOversizedDataUrls, stripVideoInputLocalSettings } from './persistence/payloadHygiene';
import type {
  CanvasNode,
  CanvasSnapshot,
  ConnectedInputs,
  ConnectedMediaRef,
  Connection,
  NodeType,
  ViewState,
} from './types';

const MAX_UNDO = 50;

// 导出给拖入落点排布用（utils/fileDrop）：多文件平铺要按各节点真实宽度算步长，
// 固定步长会让宽节点（video-input 580）压住相邻节点。
export const DEFAULT_SIZES: Record<NodeType, { width: number; height: number }> = {
  'input-image': { width: 260, height: 260 },
  'input-audio': { width: 300, height: 200 },
  // 宽高对齐 16:9 的 gen-image（nodeBoxFromDims: 280*16/9≈498），与 AI 绘图
  // 摆在一起不显小一号（用户点名一致）——同 gen-audio / script 的对齐惯例。
  'text-node': { width: 498, height: 280 },
  'gen-image': { width: 280, height: 280 },
  'gen-video': { width: 400, height: 500 },
  // 宽高对齐 16:9 的 gen-image（nodeBoxFromDims: 280*16/9≈498），与 AI 绘图
  // 摆在一起不显小一号（用户点名一致）。音频无画面、不随比例联动，固定这个框。
  'gen-audio': { width: 498, height: 280 },
  'video-input': { width: 580, height: 460 },
  'image-compare': { width: 400, height: 300 },
  'local-save': { width: 320, height: 380 },
  'preview': { width: 320, height: 260 },
  // 后处理节点比 preview 高一截：面板里要放「选项列表 + 预估 + 提交按钮」，
  // 260 高时后台配 3~4 个选项就得滚动才能看全（线上反馈）。380 能容纳 4 项不滚。
  'subtitle-erase': { width: 320, height: 380 },
  'video-upscale': { width: 320, height: 380 },
  'video-whitemodel': { width: 320, height: 380 },
  // 宽度对齐 16:9 的 gen-image（nodeBoxFromDims: 280*16/9≈498）——脚本节点通常
  // 和 AI 绘图/AI 视频排在一起，300 宽在旁边显得矮一截（用户点名对齐）。
  script: { width: 500, height: 280 },
  // 卡片只是全屏 3D 导演台的入口（同 script 的「小卡片 + 全屏面板」范式），
  // 不承载内容，比 script 矮一档即可。
  'director-desk': { width: 360, height: 240 },
};

// VIDEO_OUTPUT_NODE_TYPES 是产物类型**固定为视频**的节点类型。
//
// 为什么要显式列举而不是靠 URL 后缀猜：后处理产物的签名 URL 通常没有扩展名
// （对象 key 形如 gen/task-42-0），后缀正则一律猜不中；而 settings.kind 依赖节点
// 成功时写入，旧快照 / agent 产出的节点可能没写。节点类型是这三者里唯一始终可靠的
// 真源——去字幕、转高清、转白模的输入输出都是视频，不存在例外。
//
// 漏了这个白名单的后果（线上 bug）：去字幕产物接给转高清节点，下游报「请连接一个
// 视频」，因为视频被归进了 images。同一类误判在历史面板上修过一次（da968ee 用
// output.outputKind 收口），当时漏了画布 getConnectedInputs 这条路。
const VIDEO_OUTPUT_NODE_TYPES: ReadonlySet<NodeType> = new Set<NodeType>([
  'gen-video',
  'video-input',
  'subtitle-erase',
  'video-upscale',
  'video-whitemodel',
]);

export interface LightboxState {
  url: string;
  kind: 'image' | 'video' | 'audio';
  title?: string;
}

export interface ConnectingState {
  fromId: string;
  hoverTargetId: string | null;
}

// 音频「截取/变速」会话(见 CanvasState.audioEdit 的注释)。
// trim: start/end 是选区秒;speed: rate 是倍速。duration 进入模式时未知
// (null),由 WaveformPlayer 拿到音频时长后回填并顺手给 trim 一个默认选区。
export interface AudioEditState {
  nodeId: string;
  mode: 'trim' | 'speed';
  start: number;
  end: number;
  rate: number;
  duration: number | null;
}

// 结构性画布 op：节点/连线的增删。这是「用户动作触发局部保存」模型的收口——
// store 是所有节点/连线创建删除的唯一真源，与其在十几个调用点各自记得去
// 持久化，不如让 store 在改完状态后广播一条语义 op，由持久化层（editor 页
// 注册的 sink）翻译成对应的局部 mutation 请求。
//
// 只广播「结构性」op：新增/删除节点、新增/删除连线。节点内容/参数/几何的
// 更新（updateNode/updateNodeSettings/moveNodes/resizeNode）不在这里广播——
// 它们既被用户编辑调用、也被生成结果的后端回写和拖动每帧调用，需要在组件层
// 按 debounce/松手/「只补前端字段」等语义显式持久化，避免每帧发请求或与后端
// 回写打架。
//
// patchNode 只出现在 undo/redo 的差量里：节点前后都存在、只是字段变了。持久化层
// 对基线做 diff，只发真正变了的字段（同 persistNodeById），不再整节点覆盖服务端——
// 整节点 upsert 会把与服务端无关的旧字段也一并原样重写一遍。
export type CanvasOp =
  | { kind: 'upsertNode'; node: CanvasNode }
  | { kind: 'patchNode'; node: CanvasNode }
  | { kind: 'deleteNodes'; ids: string[] }
  | { kind: 'upsertConnection'; connection: Connection }
  | { kind: 'deleteConnection'; id: string };

let canvasOpSink: ((op: CanvasOp) => void) | null = null;

// 注册/注销持久化 sink。editor 页拿到 owner-token 后注册，离开时注销。
// 分享 workflow 的只读查看器、测试等不注册，则所有 op 都是 no-op。
export function setCanvasOpSink(sink: ((op: CanvasOp) => void) | null): void {
  canvasOpSink = sink;
}

function emitCanvasOp(op: CanvasOp): void {
  canvasOpSink?.(op);
}

// 撤销/重做时「以当前值为准」的字段：这些字段由生成/上传结果写入（后端 sink
// 或前端轮询回填），不进撤销栈，也不是用户的编辑动作。整张画布按快照回滚会把
// 别的节点刚生成好的结果一起退掉，并写回服务端（P1）。
//
// 清单按 canvasgen sink（server/internal/canvasgen/service.go 的 OnGenerationResult、
// writeback.go 的 appendHistory、repersist_apply.go）与前端轮询回填
//（GenImageNode/GenVideoNode/GenAudioNode 成功分支）实际写的字段核对：
//   - 节点顶层：content / status / taskId / progress / error；
//   - settings：history / historyIndex / assetId / pendingGen / kind / unsaved /
//     genTaskId，以及 seedance 产物的 upstreamExpire / srcGenTaskId。
// settings.text 不在清单里：文本节点的正文同时也是用户手打的内容，撤销打字要能撤回。
const SERVER_OWNED_NODE_KEYS = ['content', 'status', 'taskId', 'progress', 'error'] as const;
const SERVER_OWNED_SETTINGS_KEYS = [
  'history',
  'historyIndex',
  'assetId',
  'pendingGen',
  'kind',
  'unsaved',
  'genTaskId',
  'upstreamExpire',
  'srcGenTaskId',
] as const;

// 快照节点 + 当前节点 → 合并后的节点：几何、参数、提示词等照用快照值（这才是撤销
// 要撤的东西），服务端权威字段用当前值覆盖。
//
// 合并结果与当前节点逐字段引用相同时直接返回当前节点：快照与现状只差生成结果的
// 节点（典型：移动 B 后 A 出图，再撤销），合并后就是 A 本身，引用不变——
// emitReconcile 据此跳过它，组件也不会因为撤销而重渲染。
function keepServerOwnedFields(snap: CanvasNode, cur: CanvasNode): CanvasNode {
  if (snap === cur) return cur;
  const merged: CanvasNode = { ...snap };
  const mergedRecord = merged as unknown as Record<string, unknown>;
  const curRecord = cur as unknown as Record<string, unknown>;
  for (const key of SERVER_OWNED_NODE_KEYS) {
    if (curRecord[key] === undefined) delete mergedRecord[key];
    else mergedRecord[key] = curRecord[key];
  }
  if (snap.settings || cur.settings) {
    const curSettings = cur.settings ?? {};
    const settings: Record<string, unknown> = { ...(snap.settings ?? {}) };
    for (const key of SERVER_OWNED_SETTINGS_KEYS) {
      if (curSettings[key] === undefined) delete settings[key];
      else settings[key] = curSettings[key];
    }
    merged.settings = settings;
  }
  return shallowSameNode(merged, cur) ? cur : merged;
}

// 顶层字段与 settings 的第一层都按引用比较。settings 每次 updateNodeSettings 都会
// 换新对象，所以要再往下比一层，否则「内容没变」的节点永远认不出来。
function shallowSameNode(a: CanvasNode, b: CanvasNode): boolean {
  const ar = a as unknown as Record<string, unknown>;
  const br = b as unknown as Record<string, unknown>;
  if (!sameFieldsShallow(ar, br, 'settings')) return false;
  if (a.settings === b.settings) return true;
  return sameFieldsShallow(a.settings ?? {}, b.settings ?? {});
}

function sameFieldsShallow(
  a: Record<string, unknown>,
  b: Record<string, unknown>,
  skip?: string,
): boolean {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
  for (const key of keys) {
    if (key === skip) continue;
    if (a[key] !== b[key]) return false;
  }
  return true;
}

// 撤销/重做落地的节点列表：快照里有、当前也有的节点保留当前的服务端权威字段；
// 只在快照里有的节点（撤销一次删除而重新出现的）照用快照值——那正是用户要找回的
// 完整节点。没有节点需要合并时返回快照数组本身，保持引用稳定。
function mergeSnapshotNodes(
  snapshotNodes: CanvasNode[],
  currentById: ReadonlyMap<string, CanvasNode>,
): CanvasNode[] {
  let changed = false;
  const merged = snapshotNodes.map((node) => {
    const cur = currentById.get(node.id);
    if (!cur) return node;
    const next = keepServerOwnedFields(node, cur);
    if (next !== node) changed = true;
    return next;
  });
  return changed ? merged : snapshotNodes;
}

/**
 * 复制一个节点：内容 1:1 照搬（content / settings / 生成历史 / 提示词 / 模型与参数
 * / 尺寸），只换 id 并错开 32px，让副本看得见。
 *
 * 两处**故意不照搬**，因为它们描述的是「原节点此刻正在做的事」而不是节点内容：
 *
 *   - `taskId`：任务归属。跟着复制的话，副本会去轮询别人的任务，两个节点抢同一个
 *     结果——而后端 sink 只认原节点 id，副本拿到的是一份来路不明的拷贝。
 *   - `settings.pendingGen`：那次提交的提示词快照，跟 taskId 是一对。
 *
 * 于是进行中的状态也要落定：`generating`/`uploading` 是「有个在途任务」的意思，
 * 副本没有在途任务，所以按有没有内容落成 succeeded / idle。用户对副本的预期是
 * 「当前这个样子的一份静态拷贝」，而不是一个还在转圈的壳子。
 *
 * settings 深拷贝（不是 spread 一层）：history 数组、params 对象都是嵌套结构，
 * 共享引用的话，将来任何一处就地改写都会串到另一个节点上。
 */
/** 拷贝/复制共用的节点净化：深拷贝 settings、去掉运行时字段（taskId/pendingGen）、
 *  把瞬态 status 收敛到稳态。拷贝（原地副本）和剪贴板复制粘贴必须同一套规则——
 *  各写一套的话，剪贴板粘出来的节点会带着 generating 状态永远转圈。 */
function sanitizeNodeCopy(node: CanvasNode): CanvasNode {
  const { taskId: _dropTaskId, ...rest } = node;
  // 深拷贝顺带拒收超大 dataURL；video-input 的抽帧是本机缓存（帧在 IndexedDB
  // 按原节点 id 存），不跟进副本，也就不会经副本的 upsert 上服务端（P8）。
  const settings = node.settings
    ? cloneWithoutOversizedDataUrls(node.settings)
    : undefined;
  if (settings) {
    delete settings.pendingGen;
    stripVideoInputLocalSettings(node.type, settings);
    // 脚本节点的生成任务挂在 settings.gen 里（不是顶层 taskId）：副本带着它会去
    // 轮询原节点的任务，成功后把同一份分镜导入副本、覆盖已有内容（P11）。
    if (node.type === 'script') stripScriptGenTask(settings);
  }

  const clone: CanvasNode = { ...rest, settings };
  if (clone.status === 'generating' || clone.status === 'uploading') {
    clone.status = clone.content ? 'succeeded' : 'idle';
    clone.progress = undefined;
  }
  return clone;
}

function stripScriptGenTask(settings: Record<string, unknown>): void {
  const gen = settings.gen;
  if (gen && typeof gen === 'object' && !Array.isArray(gen)) {
    settings.gen = { ...(gen as Record<string, unknown>), taskId: 0, error: '' };
  }
}

function cloneNodeForDuplicate(node: CanvasNode, id: string): CanvasNode {
  return { ...sanitizeNodeCopy(node), id, x: node.x + 32, y: node.y + 32 };
}

// 把 before→after 的节点/连线集合差量翻译成一串结构性 op。undo/redo 会整体
// 换掉画布状态，无法预知改了哪些，只能对比前后集合逐个补 upsert/patch/delete。
// 发射顺序（先删节点、再 upsert/patch 节点、再删连线、再 upsert 连线）保证队列串行
// 执行时连线两端节点已存在。
//
// 前后都在的节点：引用相同直接跳过（没改过的节点在快照之间本来就共享引用，
// 不必两次 JSON.stringify 比较，P18）；引用不同发 patchNode，由持久化层对基线
// diff、只发变化字段。新出现的节点（撤销删除 / 重做新增）才整节点 upsert。
function emitReconcile(
  before: { nodes: CanvasNode[]; connections: Connection[] },
  after: { nodes: CanvasNode[]; connections: Connection[] },
): void {
  if (!canvasOpSink) return;
  const beforeNodes = new Map(before.nodes.map((n) => [n.id, n]));
  const afterNodes = new Map(after.nodes.map((n) => [n.id, n]));
  const removedNodeIds = [...beforeNodes.keys()].filter((id) => !afterNodes.has(id));
  if (removedNodeIds.length > 0) emitCanvasOp({ kind: 'deleteNodes', ids: removedNodeIds });
  for (const [id, node] of afterNodes) {
    const prev = beforeNodes.get(id);
    if (prev === node) continue;
    emitCanvasOp(prev ? { kind: 'patchNode', node } : { kind: 'upsertNode', node });
  }
  const beforeConns = new Map(before.connections.map((c) => [c.id, c]));
  const afterConns = new Map(after.connections.map((c) => [c.id, c]));
  for (const id of beforeConns.keys()) {
    if (!afterConns.has(id)) emitCanvasOp({ kind: 'deleteConnection', id });
  }
  for (const [id, connection] of afterConns) {
    if (!beforeConns.has(id)) emitCanvasOp({ kind: 'upsertConnection', connection });
  }
}

interface CanvasState {
  nodes: CanvasNode[];
  connections: Connection[];
  // 派生索引（P4）：id → 节点、目标节点 id → 入边（保持 connections 原顺序）。
  // 由 withDerivedIndexes 在每次 set 时随 nodes/connections 一起换新，任何写入
  // 路径（含直接 useCanvasStore.setState）都不需要、也不应该手动维护。
  // 节点组件的 selector 按 id 取（s.nodesById.get(id)），不再每次 set 都 O(N) 扫描。
  nodesById: ReadonlyMap<string, CanvasNode>;
  incomingByNode: ReadonlyMap<string, readonly Connection[]>;
  view: ViewState;
  selectedIds: Set<string>;
  undoStack: CanvasSnapshot[];
  redoStack: CanvasSnapshot[];
  lightbox: LightboxState | null;
  // 正在剪辑(trim)的视频节点 id。全画布同一时刻只允许一个剪辑会话——
  // 工具条(VideoActionsBar)和裁剪条(VideoTrimBar)分属两个组件树,
  // 「谁在剪辑」必须放 store 共享;单字段天然表达了互斥约束。
  trimmingNodeId: string | null;
  // 正在空间裁剪(crop)的视频节点 id。道理同 trimmingNodeId:裁剪层与工具条
  // 分属两处,「谁在裁剪」放 store 共享,单字段天然互斥。与 trim 也互斥——
  // 两种模式都从工具条进,进任何一种时工具条已让位,不存在同时打开的入口。
  croppingNodeId: string | null;
  // 正在宫格切分的图片节点 id。切分模式是 ImageActionsBar 的内部状态，但
  // gen-image 的 prompt 面板（另一棵组件树）在切分时必须让位——网格叠在节点上
  // 时面板还展开着会盖住格子（用户点名）。跨组件树的「谁在切分」只能放 store，
  // 同 trimmingNodeId 的先例；单字段同样天然表达了全画布同时只有一个切分会话。
  gridSplittingNodeId: string | null;
  connecting: ConnectingState | null;
  // 只读模式把所有会改状态的 action 都变成 no-op。被分享 workflow 的查看器
  // 用来原样复用 CanvasEditor 和每个 node 组件，同时阻止任何已接线的修改类
  // action（drag、resize、delete、generate、addConnection、undo/redo……）
  // 改动 store 状态。Pan/zoom、selection 和 lightbox 仍然可用，
  // 这样查看者依然能浏览和查看。
  readOnly: boolean;

  // 节点下挂面板（多角度/打光编辑器）当前打开在哪个节点上；null=没开。
  // gen-image 节点用它把自己的下方提示词面板让位隐藏——两者叠同一块空间，
  // 提示词面板(680宽)比编辑器面板宽，边缘会从编辑器两侧露出来。
  dockPanelNodeId: string | null;
  // 正在标注（涂鸦）的图片节点 id。同 gridSplittingNodeId 的道理：标注模式是
  // ImageActionsBar 的内部状态，但 gen-image 的 prompt 面板（另一棵组件树）
  // 在标注时必须让位——绘制层叠在节点上时下方还展开着输入框（用户点名要隐藏）。
  annotatingNodeId: string | null;
  // 正在音频编辑(截取/变速)的会话。截取的选区把手画在 WaveformPlayer 的波形
  // 上、模式条在 AudioActionsBar——两棵组件树共享,所以放 store;单对象天然
  // 表达了全画布同时只有一个音频编辑会话、两种模式互斥。duration 由
  // WaveformPlayer 回填(只有它知道音频时长),回填前模式条禁用提交。
  audioEdit: AudioEditState | null;
  // 正在片段重拍的视频节点 id。道理同 trimmingNodeId：重拍面板占节点正下方,
  // 与工具条/gen-video 的 prompt 面板分属不同组件树,单字段共享 + 天然互斥。
  reshootNodeId: string | null;
  // 正在智能续写(选前置片段)的视频节点 id。道理同 reshootNodeId。
  continuationNodeId: string | null;

  setReadOnly: (v: boolean) => void;
  setDockPanelNodeId: (id: string | null) => void;
  setLightbox: (lb: LightboxState | null) => void;
  setTrimmingNode: (id: string | null) => void;
  setAudioEdit: (v: AudioEditState | null) => void;
  setCroppingNode: (id: string | null) => void;
  setReshootNode: (id: string | null) => void;
  setContinuationNode: (id: string | null) => void;
  setGridSplittingNode: (id: string | null) => void;
  setAnnotatingNode: (id: string | null) => void;
  setConnecting: (c: ConnectingState | null) => void;
  setConnectingHoverTarget: (id: string | null) => void;
  /**
   * 整快照落地。默认语义是「换一张画布」：选中、undo/redo 栈全清。
   *
   * `preserveUiState` 用于**同一张画布**的整拉——那不是换画布，是把本地对齐到服务端最新状态：
   * 保留选中（按新节点列表剪枝，服务端已不存在的节点不能还留在选中里），否则会把用户正开着的
   * 参数面板收起来；默认也保留 undo/redo 栈，撤销撤的仍是「我自己做过的那几步」。
   *
   * `resetHistory` 额外清空 undo/redo 栈。外部（CLI）变更整拉（`useExternalChanges`）会传它：
   * 撤销栈里记录的是整拉前的画布状态，撤销会把外部新增的节点删掉。
   */
  load: (snapshot: Partial<CanvasSnapshot>, options?: { preserveUiState?: boolean; resetHistory?: boolean }) => void;
  addNode: (type: NodeType, worldPosition: { x: number; y: number }) => string;
  updateNode: (id: string, patch: Partial<CanvasNode>) => void;
  updateNodeSettings: (id: string, patch: Record<string, unknown>) => void;
  moveNodes: (ids: string[], dx: number, dy: number) => void;
  resizeNode: (id: string, width: number, height: number) => void;
  deleteNodes: (ids: string[]) => void;
  duplicateNodes: (ids: string[]) => void;

  /** 「复制节点」的取数侧：选中节点（净化后）+ 仅这批节点内部的连线。
   *  出/入边的另一端不在剪贴板里，带上就是死引用，所以不带。空选择返回 null。 */
  buildCopyPayload: (ids: string[]) => { nodes: CanvasNode[]; connections: Connection[] } | null;
  /** 「粘贴节点」的落地侧：重发 id、重映射 @ 提及、这批节点的包围盒中心对齐
   *  到 at（与 addNode 的中心放置语义一致）。返回新节点 id 列表。 */
  pasteNodes: (
    nodes: CanvasNode[],
    connections: Connection[],
    at: { x: number; y: number },
  ) => string[];

  /** 宫格切分的落地侧：按给定矩形批量建 input-image 节点，并各自从 sourceId
   *  连一条入边（用户点名要连回源图——来源可追溯）。整批一次 commit：25 格
   *  切分按一次撤销收回，而不是按 Ctrl+Z 二十五下。返回新节点 id（与 rects
   *  同序，调用方按下标对应格子上传）。 */
  addLinkedInputImageNodes: (
    sourceId: string,
    rects: Array<{ x: number; y: number; width: number; height: number }>,
  ) => string[];

  addConnection: (from: string, to: string, inputType?: string) => void;
  removeConnection: (id: string) => void;

  selectNode: (id: string | null, additive?: boolean) => void;
  selectMany: (ids: string[]) => void;
  clearSelection: () => void;

  setView: (view: ViewState) => void;
  // 把画布视口平移到指定节点居中（保持当前 zoom）。给 agent 聊天里的
  // 「定位到画布节点」chip 用——纯视图操作，不改节点数据，readOnly 下也可用
  //（与 pan/zoom 同一类豁免）。
  centerNode: (id: string) => void;

  commit: () => void;
  undo: () => void;
  redo: () => void;

  getSnapshot: () => CanvasSnapshot;
  getConnectedInputs: (nodeId: string) => ConnectedInputs;
}

// 索引按数组引用记忆：同一个 nodes / connections 数组只建一次索引。撤销栈里的
// 快照数组被原样放回 store 时（撤销无需合并的情况、重做等）直接复用。
const nodesIndexCache = new WeakMap<CanvasNode[], ReadonlyMap<string, CanvasNode>>();
const incomingIndexCache = new WeakMap<Connection[], ReadonlyMap<string, readonly Connection[]>>();

function indexNodes(nodes: CanvasNode[]): ReadonlyMap<string, CanvasNode> {
  let index = nodesIndexCache.get(nodes);
  if (!index) {
    index = new Map(nodes.map((n) => [n.id, n]));
    nodesIndexCache.set(nodes, index);
  }
  return index;
}

function indexIncoming(connections: Connection[]): ReadonlyMap<string, readonly Connection[]> {
  let index = incomingIndexCache.get(connections);
  if (!index) {
    const map = new Map<string, Connection[]>();
    for (const c of connections) {
      const list = map.get(c.to);
      if (list) list.push(c);
      else map.set(c.to, [c]);
    }
    index = map;
    incomingIndexCache.set(connections, index);
  }
  return index;
}

const EMPTY_CONNECTIONS: readonly Connection[] = [];

// 集中维护派生索引的 set 包装：写入里带了 nodes / connections，就在同一次 set 里
// 换上对应索引（订阅方看不到数组与索引不一致的中间态）。同时替换 api.setState——
// Sidebar 自动排布、测试等处直接 useCanvasStore.setState({ nodes }) 也走这里；
// 只在各个 action 里手动维护的话，这类写入会让索引悄悄过期。
const withDerivedIndexes =
  (config: StateCreator<CanvasState>): StateCreator<CanvasState> =>
  (set, get, api) => {
    const indexedSet: typeof set = (partial, replace) => {
      const resolved = typeof partial === 'function' ? partial(get()) : partial;
      const next: Partial<CanvasState> = { ...resolved };
      if (resolved.nodes) next.nodesById = indexNodes(resolved.nodes);
      if (resolved.connections) next.incomingByNode = indexIncoming(resolved.connections);
      (set as (p: Partial<CanvasState>, r?: boolean) => void)(next, replace);
    };
    api.setState = indexedSet;
    return config(indexedSet, get, api);
  };

const INITIAL_NODES: CanvasNode[] = [];
const INITIAL_CONNECTIONS: Connection[] = [];

export const useCanvasStore = create<CanvasState>()(withDerivedIndexes((set, get) => ({
  nodes: INITIAL_NODES,
  connections: INITIAL_CONNECTIONS,
  nodesById: indexNodes(INITIAL_NODES),
  incomingByNode: indexIncoming(INITIAL_CONNECTIONS),
  view: { x: 0, y: 0, zoom: 1 },
  selectedIds: new Set<string>(),
  undoStack: [],
  redoStack: [],
  lightbox: null,
  trimmingNodeId: null,
  audioEdit: null,
  croppingNodeId: null,
  gridSplittingNodeId: null,
  annotatingNodeId: null,
  reshootNodeId: null,
  continuationNodeId: null,
  connecting: null,
  readOnly: false,
  dockPanelNodeId: null,
  setDockPanelNodeId: (id) => set({ dockPanelNodeId: id }),

  setReadOnly: (v) => {
    // 同时翻转 api 层的写入开关——store 的 readOnly 能挡住修改 store 的
    // action（updateNode 等），但没有这一句的话，网络侧的修改类调用
    //（submitCanvasTask / uploadAsset / 画布局部 mutation / ……）仍会发出并在服务端执行。
    // 一个 setter，让两层保持同步。
    setApiWritesBlocked(v);
    set({ readOnly: v });
  },
  setLightbox: (lb) => set({ lightbox: lb }),
  // 进入剪辑要挡 readOnly(它会派生出转码上传/建节点等写操作);退出(null)
  // 任何时候都放行,免得只读切换时把会话卡死在打开态。
  setTrimmingNode: (id) => {
    if (id !== null && get().readOnly) return;
    set({ trimmingNodeId: id });
  },
  // 同 setTrimmingNode:进入音频编辑挡 readOnly(提交派生转码上传/建节点等
  // 写操作),退出(null)任何时候放行。
  setAudioEdit: (v) => {
    if (v !== null && get().readOnly) return;
    set({ audioEdit: v });
  },
  // 同 setTrimmingNode:进入挡 readOnly(裁剪派生转码上传/建节点等写操作),
  // 退出(null)任何时候放行。
  setCroppingNode: (id) => {
    if (id !== null && get().readOnly) return;
    set({ croppingNodeId: id });
  },
  // 同 setTrimmingNode：进入挡 readOnly（切分派生裁剪上传/建节点等写操作），
  // 退出(null)任何时候放行。
  setGridSplittingNode: (id) => {
    if (id !== null && get().readOnly) return;
    set({ gridSplittingNodeId: id });
  },
  // 同上：进入挡 readOnly（标注保存派生合成上传/建节点等写操作），退出放行。
  setAnnotatingNode: (id) => {
    if (id !== null && get().readOnly) return;
    set({ annotatingNodeId: id });
  },
  // 同 setTrimmingNode：进入挡 readOnly（重拍提交派生切片上传/铺节点等写操作），
  // 退出(null)任何时候放行。
  setReshootNode: (id) => {
    if (id !== null && get().readOnly) return;
    set({ reshootNodeId: id });
  },
  // 同 setReshootNode：进入挡 readOnly（续写提交派生切片上传/铺节点等写操作），
  // 退出(null)任何时候放行。
  setContinuationNode: (id) => {
    if (id !== null && get().readOnly) return;
    set({ continuationNodeId: id });
  },
  setConnecting: (c) => set({ connecting: c }),
  setConnectingHoverTarget: (id) =>
    set((s) => (s.connecting ? { connecting: { ...s.connecting, hoverTargetId: id } } : {})),

  load: (snapshot, options) => {
    const nodes = snapshot.nodes ?? [];
    const base = {
      nodes,
      connections: snapshot.connections ?? [],
      view: snapshot.view ?? { x: 0, y: 0, zoom: 1 },
    };
    if (!options?.preserveUiState) {
      set({ ...base, selectedIds: new Set<string>(), undoStack: [], redoStack: [] });
      return;
    }
    // 保留模式：选中集按新节点剪枝——服务端已不存在的节点若还留在 selectedIds 里，
    // 后续任何「对选中批量操作」都会带上一个已经不存在的 id。
    const alive = new Set(nodes.map((n) => n.id));
    const st = useCanvasStore.getState();
    const selectedIds = new Set([...st.selectedIds].filter((id) => alive.has(id)));
    // 指向已被删除节点的编辑会话一律关闭，否则浮层会挂在一个不存在的节点上。
    const keep = (id: string | null) => (id && alive.has(id) ? id : null);
    set({
      ...base,
      selectedIds,
      croppingNodeId: keep(st.croppingNodeId),
      trimmingNodeId: keep(st.trimmingNodeId),
      gridSplittingNodeId: keep(st.gridSplittingNodeId),
      annotatingNodeId: keep(st.annotatingNodeId),
      reshootNodeId: keep(st.reshootNodeId),
      continuationNodeId: keep(st.continuationNodeId),
      dockPanelNodeId: keep(st.dockPanelNodeId),
      audioEdit: st.audioEdit && alive.has(st.audioEdit.nodeId) ? st.audioEdit : null,
      ...(options.resetHistory ? { undoStack: [], redoStack: [] } : {}),
    });
  },

  addNode: (type, worldPosition) => {
    if (get().readOnly) return '';
    const id = `n-${nanoid(8)}`;
    const size = DEFAULT_SIZES[type];
    const node: CanvasNode = {
      id,
      type,
      x: worldPosition.x - size.width / 2,
      y: worldPosition.y - size.height / 2,
      width: size.width,
      height: size.height,
      settings: {},
      status: 'idle',
    };
    get().commit();
    set((s) => ({
      nodes: [...s.nodes, node],
      selectedIds: new Set([id]),
    }));
    emitCanvasOp({ kind: 'upsertNode', node });
    return id;
  },

  updateNode: (id, patch) => {
    if (get().readOnly) return;
    set((s) => ({
      nodes: s.nodes.map((n) => (n.id === id ? { ...n, ...patch } : n)),
    }));
  },

  updateNodeSettings: (id, patch) => {
    if (get().readOnly) return;
    set((s) => ({
      nodes: s.nodes.map((n) =>
        n.id === id ? { ...n, settings: { ...(n.settings ?? {}), ...patch } } : n,
      ),
    }));
  },

  moveNodes: (ids, dx, dy) => {
    if (get().readOnly) return;
    set((s) => {
      const idSet = new Set(ids);
      return {
        nodes: s.nodes.map((n) =>
          idSet.has(n.id) ? { ...n, x: n.x + dx, y: n.y + dy } : n,
        ),
      };
    });
  },

  resizeNode: (id, width, height) => {
    if (get().readOnly) return;
    set((s) => ({
      nodes: s.nodes.map((n) => (n.id === id ? { ...n, width, height } : n)),
    }));
  },

  deleteNodes: (ids) => {
    if (get().readOnly) return;
    if (ids.length === 0) return;
    get().commit();
    const present = new Set(get().nodes.map((n) => n.id));
    const existing = ids.filter((id) => present.has(id));
    set((s) => {
      const idSet = new Set(ids);
      return {
        nodes: s.nodes.filter((n) => !idSet.has(n.id)),
        connections: s.connections.filter((c) => !idSet.has(c.from) && !idSet.has(c.to)),
        selectedIds: new Set<string>(),
      };
    });
    // 后端 DeleteNodes 会一并删掉两端指向被删节点的连线，前端无需再单独发连线删除。
    if (existing.length > 0) emitCanvasOp({ kind: 'deleteNodes', ids: existing });
  },

  duplicateNodes: (ids) => {
    if (get().readOnly) return;
    if (ids.length === 0) return;
    get().commit();
    const src = get().nodes;
    const newNodes: CanvasNode[] = [];
    const newIds = new Set<string>();
    // 原 id → 副本 id。连线重建和提及重写都要按它翻译。
    const idMap = new Map<string, string>();
    for (const srcId of ids) {
      const node = src.find((n) => n.id === srcId);
      if (!node) continue;
      const newId = `n-${nanoid(8)}`;
      idMap.set(srcId, newId);
      newNodes.push(cloneNodeForDuplicate(node, newId));
      newIds.add(newId);
    }
    if (newNodes.length === 0) return;

    // 提示词里的 @ 提及绑的是上游 nodeId。同批复制的节点之间连线会重建到副本上，
    // 提及也得跟着改指副本，否则副本引用的还是原节点、而副本并没连着它，
    // 断线清理会把这些标记默默删掉——用户的提示词就被改了。
    for (const node of newNodes) {
      const s = node.settings;
      if (!s) continue;
      for (const key of ['prompt', 'videoPrompt']) {
        if (typeof s[key] === 'string') s[key] = remapMentionNodeIds(s[key] as string, idMap);
      }
    }

    // 连线：只复制**入边**——副本要能立刻重跑同样的生成，就得连着同样的输入。
    //   - 来自这批之外的上游：from 保持原节点，即副本与原节点共享同一个输入源；
    //   - 来自这批之内的节点（多选复制）：from 映射到对应副本，让副本自成一套。
    // 出边不复制：那会让原本的下游节点凭空多出一个输入源，几乎从不是用户想要的；
    // 副本产出接给谁，交给用户自己连。
    const newConnections: Connection[] = [];
    for (const c of get().connections) {
      const to = idMap.get(c.to);
      if (!to) continue;
      newConnections.push({
        id: `e-${nanoid(8)}`,
        from: idMap.get(c.from) ?? c.from,
        to,
        inputType: c.inputType,
      });
    }

    set((s) => ({
      nodes: [...s.nodes, ...newNodes],
      connections: [...s.connections, ...newConnections],
      selectedIds: newIds,
    }));
    for (const node of newNodes) emitCanvasOp({ kind: 'upsertNode', node });
    for (const connection of newConnections) emitCanvasOp({ kind: 'upsertConnection', connection });
  },

  buildCopyPayload: (ids) => {
    const idSet = new Set(ids);
    const nodes = get()
      .nodes.filter((n) => idSet.has(n.id))
      .map((n) => sanitizeNodeCopy(n));
    if (nodes.length === 0) return null;
    const connections = get()
      .connections.filter((c) => idSet.has(c.from) && idSet.has(c.to))
      .map((c) => ({ ...c }));
    return { nodes, connections };
  },

  pasteNodes: (nodes, connections, at) => {
    if (get().readOnly) return [];
    if (nodes.length === 0) return [];
    get().commit();

    // 包围盒中心对齐到 at。同一份 payload 可以粘多次（mirror/剪贴板都是持久的），
    // settings 必须深拷贝——浅拷贝的话两次粘贴共享同一个对象，提及重写会互相污染。
    const minX = Math.min(...nodes.map((n) => n.x));
    const minY = Math.min(...nodes.map((n) => n.y));
    const maxX = Math.max(...nodes.map((n) => n.x + n.width));
    const maxY = Math.max(...nodes.map((n) => n.y + n.height));
    const offsetX = at.x - (minX + (maxX - minX) / 2);
    const offsetY = at.y - (minY + (maxY - minY) / 2);

    const idMap = new Map<string, string>();
    const newNodes: CanvasNode[] = nodes.map((n) => {
      const newId = `n-${nanoid(8)}`;
      idMap.set(n.id, newId);
      return {
        ...n,
        id: newId,
        x: n.x + offsetX,
        y: n.y + offsetY,
        settings: n.settings
          ? (JSON.parse(JSON.stringify(n.settings)) as Record<string, unknown>)
          : undefined,
      };
    });

    // 提示词 @ 提及重写到副本 id——与 duplicateNodes 同理：批内引用跟着连线走，
    // 批外引用保持原样（同项目粘贴时原节点还在；跨项目就是悬空，断线清理会收掉）。
    for (const node of newNodes) {
      const s = node.settings;
      if (!s) continue;
      for (const key of ['prompt', 'videoPrompt']) {
        if (typeof s[key] === 'string') s[key] = remapMentionNodeIds(s[key] as string, idMap);
      }
    }

    const newConnections: Connection[] = [];
    for (const c of connections) {
      const from = idMap.get(c.from);
      const to = idMap.get(c.to);
      // 防御：payload 被篡改/旧版本混入外部连线时丢弃，而不是让画布出现悬空线。
      if (!from || !to) continue;
      newConnections.push({ id: `e-${nanoid(8)}`, from, to, inputType: c.inputType });
    }

    const newIds = new Set(idMap.values());
    set((s) => ({
      nodes: [...s.nodes, ...newNodes],
      connections: [...s.connections, ...newConnections],
      selectedIds: newIds,
    }));
    for (const node of newNodes) emitCanvasOp({ kind: 'upsertNode', node });
    for (const connection of newConnections) emitCanvasOp({ kind: 'upsertConnection', connection });
    return [...newIds];
  },

  addLinkedInputImageNodes: (sourceId, rects) => {
    if (get().readOnly) return [];
    if (rects.length === 0) return [];
    if (!get().nodes.some((n) => n.id === sourceId)) return [];
    get().commit();
    const newNodes: CanvasNode[] = rects.map((r) => ({
      id: `n-${nanoid(8)}`,
      type: 'input-image',
      x: r.x,
      y: r.y,
      width: r.width,
      height: r.height,
      settings: {},
      status: 'idle',
    }));
    const newConnections: Connection[] = newNodes.map((n) => ({
      id: `e-${nanoid(8)}`,
      from: sourceId,
      to: n.id,
    }));
    set((s) => ({
      nodes: [...s.nodes, ...newNodes],
      connections: [...s.connections, ...newConnections],
      selectedIds: new Set(newNodes.map((n) => n.id)),
    }));
    for (const node of newNodes) emitCanvasOp({ kind: 'upsertNode', node });
    for (const connection of newConnections) emitCanvasOp({ kind: 'upsertConnection', connection });
    return newNodes.map((n) => n.id);
  },

  addConnection: (from, to, inputType) => {
    if (get().readOnly) return;
    if (from === to) return;
    const existing = get().connections.find(
      (c) => c.from === from && c.to === to && c.inputType === inputType,
    );
    if (existing) return;
    get().commit();
    const connection: Connection = { id: `e-${nanoid(8)}`, from, to, inputType };
    set((s) => ({
      connections: [...s.connections, connection],
    }));
    emitCanvasOp({ kind: 'upsertConnection', connection });
  },

  removeConnection: (id) => {
    if (get().readOnly) return;
    const present = get().connections.some((c) => c.id === id);
    get().commit();
    set((s) => ({
      connections: s.connections.filter((c) => c.id !== id),
    }));
    if (present) emitCanvasOp({ kind: 'deleteConnection', id });
  },

  selectNode: (id, additive) =>
    set((s) => {
      if (id === null) return { selectedIds: new Set<string>() };
      if (additive) {
        const next = new Set(s.selectedIds);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        return { selectedIds: next };
      }
      return { selectedIds: new Set([id]) };
    }),

  selectMany: (ids) => set({ selectedIds: new Set(ids) }),

  clearSelection: () => set({ selectedIds: new Set<string>() }),

  setView: (view) => set({ view }),

  centerNode: (id) => {
    const node = get().nodes.find((n) => n.id === id);
    if (!node) return;
    const { zoom } = get().view;
    // 视口尺寸用「窗口减侧栏」近似——与 Minimap 的 viewport 矩形同一套算法，
    // 两处对不上的话小地图框出来的中心和这里定位到的中心会互相矛盾。
    // world→screen 变换是 screen = world*zoom + view.x（见 CanvasEditorPage
    // 的 anchor 计算），反解出让节点中心落在视口中心的 view 偏移。
    const vpW = window.innerWidth - 56;
    const vpH = window.innerHeight - 56;
    const cx = node.x + node.width / 2;
    const cy = node.y + node.height / 2;
    set({ view: { x: vpW / 2 - cx * zoom, y: vpH / 2 - cy * zoom, zoom } });
  },

  commit: () => {
    if (get().readOnly) return;
    set((s) => ({
      undoStack: [
        ...s.undoStack,
        { nodes: s.nodes, connections: s.connections, view: s.view },
      ].slice(-MAX_UNDO),
      redoStack: [],
    }));
  },

  // undo/redo 不是把整张画布原样回滚：快照与当前都有的节点，生成结果等服务端
  // 权威字段保留当前值（见 keepServerOwnedFields）。否则「移动 B → A 出图 → 撤销」
  // 会把 A 的结果一起撤掉，并经 emitReconcile 写回服务端（P1）。
  undo: () => {
    const before = get();
    if (before.readOnly || before.undoStack.length === 0) return;
    const prev = before.undoStack[before.undoStack.length - 1];
    const after = { ...prev, nodes: mergeSnapshotNodes(prev.nodes, before.nodesById) };
    set((s) => ({
      nodes: after.nodes,
      connections: after.connections,
      view: after.view,
      undoStack: s.undoStack.slice(0, -1),
      redoStack: [
        ...s.redoStack,
        { nodes: s.nodes, connections: s.connections, view: s.view },
      ],
    }));
    emitReconcile(before, after);
  },

  redo: () => {
    const before = get();
    if (before.readOnly || before.redoStack.length === 0) return;
    const next = before.redoStack[before.redoStack.length - 1];
    const after = { ...next, nodes: mergeSnapshotNodes(next.nodes, before.nodesById) };
    set((s) => ({
      nodes: after.nodes,
      connections: after.connections,
      view: after.view,
      redoStack: s.redoStack.slice(0, -1),
      undoStack: [
        ...s.undoStack,
        { nodes: s.nodes, connections: s.connections, view: s.view },
      ],
    }));
    emitReconcile(before, after);
  },

  getSnapshot: () => ({
    nodes: get().nodes,
    connections: get().connections,
    view: get().view,
  }),

  getConnectedInputs: (nodeId) => connectedInputsOf(get(), nodeId),
})));

// getConnectedInputs 的纯函数本体：组件订阅（useConnectedInputs）要对传入的
// state 快照求值，不能走 get()。
function connectedInputsOf(
  state: Pick<CanvasState, 'nodesById' | 'incomingByNode'>,
  nodeId: string,
): ConnectedInputs {
  const incoming = state.incomingByNode.get(nodeId) ?? EMPTY_CONNECTIONS;
  const images: string[] = [];
  const texts: string[] = [];
  const videos: string[] = [];
  const audios: string[] = [];
  const refs: ConnectedMediaRef[] = [];
  for (const conn of incoming) {
    const src = state.nodesById.get(conn.from);
    if (!src) continue;
    if (src.type === 'text-node') {
      const t = (src.settings?.text as string | undefined) ?? '';
      if (t) texts.push(t);
      continue;
    }
    const url = src.content;
    if (typeof url === 'string' && /^https?:\/\//.test(url)) {
      // 只取用户自定义名（与 NodeShell.displayLabelFor 的 customName 同源），不回退
      // 类型默认名——store 是纯逻辑，不 import 组件文件。
      const name = ((src.settings?.name as string | undefined) ?? '').trim() || undefined;
      // settings.kind（'image' | 'video'）是节点自报的产物类型。preview 是通用媒体
      // 容器必须靠它；后处理节点（去字幕/转高清）成功时也会写它，所以这里不再限定
      // 只读 preview 的——限定过头曾导致去字幕产物被当成图片（见下方 WHY）。
      const declaredKind = src.settings?.kind as string | undefined;
      // refs 与三个数组在同一分支里 push——分类与顺序天然一致,@ 提及序列化的
      // 「图片N」下标才能对上提交给后端的 images[N-1]。
      // gen-audio 产物的签名 URL 无扩展名（对象 key 形如 gen/task-N-0），只靠
      // 扩展名正则会漏进 images——节点类型 / declaredKind 必须先兜住。
      // （与后端 canvasinputs.Connected 逐字同构）
      if (src.type === 'input-audio' || src.type === 'gen-audio' || declaredKind === 'audio' || /\.(mp3|wav|aac|ogg|flac|m4a)(\?|$)/i.test(url)) {
        audios.push(url);
        refs.push({ nodeId: src.id, kind: 'audio', url, name });
      } else if (
        VIDEO_OUTPUT_NODE_TYPES.has(src.type) ||
        declaredKind === 'video' ||
        /\.(mp4|webm|mov)(\?|$)/i.test(url)
      ) {
        videos.push(url);
        refs.push({ nodeId: src.id, kind: 'video', url, name });
      } else {
        images.push(url);
        refs.push({ nodeId: src.id, kind: 'image', url, name });
      }
    }
  }
  return { images, texts, videos, audios, refs };
}

function sameStrings(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((v, i) => v === b[i]);
}

function sameConnectedInputs(a: ConnectedInputs, b: ConnectedInputs): boolean {
  return (
    sameStrings(a.images, b.images) &&
    sameStrings(a.texts, b.texts) &&
    sameStrings(a.videos, b.videos) &&
    sameStrings(a.audios, b.audios) &&
    a.refs.length === b.refs.length &&
    a.refs.every((r, i) => {
      const o = b.refs[i];
      return r.nodeId === o.nodeId && r.kind === o.kind && r.url === o.url && r.name === o.name;
    })
  );
}

/**
 * 订阅式的 getConnectedInputs：连线增删、上游节点内容/改名都会触发重渲染，
 * 内容不变时返回同一个对象引用（下游 useMemo/effect 依赖才稳定）。
 *
 * 渲染期直接调 `getConnectedInputs(id)`（订阅的是稳定的函数引用）不会因连线
 * 变化重渲染——撤销一条连线、agent 增删连线后，节点上的参考图/输入数一直是
 * 旧的，只能等别的无关更新顺带刷新。
 */
export function useConnectedInputs(nodeId: string): ConnectedInputs {
  const cacheRef = useRef<ConnectedInputsCache | null>(null);
  return useCanvasStore((s) => {
    // 只读本节点的入边和对应上游节点（索引取，O(入度)）。入边数组与每个上游节点
    // 的引用都没变时直接返回上次结果，连临时数组都不分配——平移、拖动别的节点、
    // 在别处打字时，每次 set 都会把这个 selector 跑一遍。
    const incoming = s.incomingByNode.get(nodeId) ?? EMPTY_CONNECTIONS;
    const prev = cacheRef.current;
    if (
      prev &&
      prev.incoming === incoming &&
      incoming.every((c, i) => s.nodesById.get(c.from) === prev.sources[i])
    ) {
      return prev.value;
    }
    const next = connectedInputsOf(s, nodeId);
    const value = prev && sameConnectedInputs(prev.value, next) ? prev.value : next;
    cacheRef.current = { incoming, sources: incoming.map((c) => s.nodesById.get(c.from)), value };
    return value;
  });
}

interface ConnectedInputsCache {
  incoming: readonly Connection[];
  sources: Array<CanvasNode | undefined>;
  value: ConnectedInputs;
}
