import { create } from 'zustand';
import { nanoid } from 'nanoid';
import { setApiWritesBlocked } from '@/shared/apiGuards';
import { isNodeInteracting, onInteractionEnd } from './interactionRegistry';
import { applyMergePatch, isPlainRecord } from './mergePatch';
import { remapMentionNodeIds } from './utils/mentions';
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
export type CanvasOp =
  | { kind: 'upsertNode'; node: CanvasNode }
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

// ── 远端 op（团队并发编辑）────────────────────────────────────────────────
//
// 队友的每一次画布写入都由服务端序列化后带着数据广播回来（canvas_changed 帧的
// op 字段），本端把它合并进 store 而不是整拉快照——整拉会清掉选中/undo 栈，
// 把正在编辑的面板收起来（一期观看端就是这个体感）。
//
// 入参是**解析后的 op 对象**而不是原始帧：帧字段是 snake_case 且随后端演进，
// 帧→op 的翻译收在页面接线层（T3）一处，store 只认这个稳定契约。

/** 远端节点几何补丁。字段与后端 canvasstate.NodeGeometryPatch 一一对应，缺省=不改。 */
export interface RemoteNodeGeometry {
  id: string;
  x?: number;
  y?: number;
  width?: number;
  height?: number;
}

export type RemoteCanvasOp =
  | { kind: 'upsertNode'; node: CanvasNode }
  | { kind: 'patchNode'; nodeId: string; patch: Record<string, unknown> }
  | { kind: 'deleteNodes'; nodeIds: string[] }
  | { kind: 'patchPositions'; positions: RemoteNodeGeometry[] }
  | { kind: 'upsertConnection'; connection: Connection }
  | { kind: 'deleteConnection'; connectionId: string };

/**
 * 远端 op 落地后回吐给持久化层的**服务端动作本身**（不是合并后的 store 节点）。
 *
 * 为什么必须是 op 而不是 store 里合并完的节点：store 节点 = 服务端状态 + 本地
 * 尚未发出的编辑（在 debounce 队列里、或者根本还没触发持久化）。把它当基线，
 * 基线就被推到了一个服务端并不持有的状态，下一次 persistNodeById 的 diff 会
 * 算出「没有变更」——本地那些编辑于是永远发不出去，而且一声不响。
 * 所以这里只回吐服务端确实做过的那一步，由持久化层拿**自己的基线**去合并。
 *
 * null = 该节点在服务端已被删除。
 */
export type RemoteNodeStateOp =
  | { kind: 'node'; node: CanvasNode }
  | { kind: 'patch'; patch: Record<string, unknown> };

export type RemoteNodeStateSink = (nodeId: string, op: RemoteNodeStateOp | null) => void;

let remoteNodeStateSink: RemoteNodeStateSink | null = null;

export function setRemoteNodeStateSink(sink: RemoteNodeStateSink | null): void {
  remoteNodeStateSink = sink;
}

/**
 * 「该节点还有哪些本地字段没发出去」的查询口，由持久化层注册（返回
 * scheduleNodePatch 队列里该节点 pending 的 patch，没有则 null）。
 *
 * 只有一个用途：交互结束 flush 挂起的远端 patch 时，把与本地 pending 重叠的
 * 字段剔掉。用户在文本节点里打字期间队友也改了同一段文字，本端 blur 后提交的
 * 值在服务端更晚落地、LWW 序上应该赢；而挂起的那帧是**打字之前**的旧值，直接
 * 落地会把用户刚打的字换成旧内容，且这次覆盖不会被任何后续广播纠正（自己发出
 * 的 op 回来时按 source_mark 跳过）。
 */
export type PendingLocalPatchProvider = (nodeId: string) => Record<string, unknown> | null;

let pendingLocalPatchProvider: PendingLocalPatchProvider | null = null;

export function setPendingLocalPatchProvider(provider: PendingLocalPatchProvider | null): void {
  pendingLocalPatchProvider = provider;
}

/**
 * 本地正在交互（拖拽中 / 文本聚焦）的节点上挂起的远端 op。
 *
 * 为什么是 defer 而不是丢弃：本地交互结束后，自己的写会走 mutation 发到服务端、
 * 再被广播回来，时序上晚于这些挂起的远端帧——先落队友的、再落自己的，LWW 自然
 * 收敛到「后提交者胜」。反过来，如果挂起期间把远端帧丢掉，队友那次改动在本端
 * 永远不会出现（服务端有、我这儿没有），只能靠整拉兜底才补得回来。
 *
 * per-node 只留一条：patch 按字段合并（同字段后到覆盖先到 = 帧序 LWW），整节点
 * upsert 直接作废此前累积的 patch（它本来就是整替换语义）。
 */
interface DeferredRemoteNodeOp {
  /** 最近一次远端整节点替换的结果；null=期间只来过增量 patch。 */
  node: CanvasNode | null;
  /** 其后累积的字段级 patch（含 patchPositions 折叠进来的几何字段）。 */
  patch: Record<string, unknown> | null;
}

const deferredRemoteOps = new Map<string, DeferredRemoteNodeOp>();

/** 丢弃全部挂起的远端 op。整快照重载（load / 版本缺口兜底）时必须调用——
 *  快照已经包含了这些改动，再落一遍就是拿旧帧覆盖新状态。 */
export function clearDeferredRemoteOps(): void {
  deferredRemoteOps.clear();
}

function deferRemoteNode(nodeId: string, node: CanvasNode | null, patch: Record<string, unknown> | null): void {
  const existing = deferredRemoteOps.get(nodeId);
  if (node) {
    // 整替换作废先前累积的增量。
    deferredRemoteOps.set(nodeId, { node, patch: null });
    return;
  }
  if (!patch) return;
  if (!existing) {
    deferredRemoteOps.set(nodeId, { node: null, patch });
    return;
  }
  if (existing.node) {
    deferredRemoteOps.set(nodeId, { node: applyMergePatch(existing.node, patch) as CanvasNode, patch: null });
    return;
  }
  deferredRemoteOps.set(nodeId, {
    node: null,
    // 两条 patch 的**合成**：后到的 key 覆盖先到的（含 null 删键本身也是一个
    // 待应用的动作，原样保留），与 applyMergePatch 的「应用」不是一回事，但
    // 对象递归这一层规则相同，可以复用。
    patch: mergeDeferredPatch(existing.patch ?? {}, patch),
  });
}

// 合成两条待应用的 patch。与 applyMergePatch 的区别只在 null：这里 null 是
// 「稍后要删这个 key」的指令，必须留在合成结果里，不能当场把 key 删掉。
function mergeDeferredPatch(
  earlier: Record<string, unknown>,
  later: Record<string, unknown>,
): Record<string, unknown> {
  const merged: Record<string, unknown> = { ...earlier };
  for (const [key, value] of Object.entries(later)) {
    const current = merged[key];
    merged[key] = isPlainRecord(current) && isPlainRecord(value)
      ? mergeDeferredPatch(current, value)
      : value;
  }
  return merged;
}

/**
 * 从远端 patch 里剔除本端仍占着的字段（递归到叶子）。
 * 见 PendingLocalPatchProvider 的说明。返回 null 表示整条 patch 都被本地占着，
 * 无需落地。
 */
function stripLocallyOwnedFields(
  patch: Record<string, unknown>,
  local: Record<string, unknown>,
): Record<string, unknown> | null {
  const kept: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(patch)) {
    const localValue = local[key];
    if (localValue === undefined) {
      kept[key] = value;
      continue;
    }
    if (isPlainRecord(value) && isPlainRecord(localValue)) {
      const nested = stripLocallyOwnedFields(value, localValue);
      if (nested) kept[key] = nested;
      continue;
    }
    // 同一个叶子字段两边都改过 → 本地赢（它已经/即将发到服务端，序更晚）。
  }
  return Object.keys(kept).length > 0 ? kept : null;
}

/** 远端 patch 落地前统一过一道本端保护：**挂起 flush 与直接落地共用同一个查询点**，
 *  否则「交互已结束、请求还在飞」那段窗口里的远端帧照样能覆盖已提交字段。 */
function stripAgainstLocal(nodeId: string, patch: Record<string, unknown>): Record<string, unknown> | null {
  const local = pendingLocalPatchProvider?.(nodeId) ?? null;
  return local ? stripLocallyOwnedFields(patch, local) : patch;
}

/** 远端整节点替换落地后，把本端仍占着的字段盖回去（store 侧；基线仍记服务端那份）。 */
function overlayLocalPending(nodeId: string, node: CanvasNode): CanvasNode {
  const local = pendingLocalPatchProvider?.(nodeId) ?? null;
  return local ? (applyMergePatch(node, local) as CanvasNode) : node;
}

// ── 瞬态坐标覆盖层 ──────────────────────────────────────────────────────
//
// 队友拖拽途中的实时坐标**不写进 nodes[]**，单独放一层。理由见
// applyTransientPositions 的注释：nodes[] 是 persistNodeById 的 diff 面，写进去
// 就等于让本端把队友的中间坐标当成自己的改动写回服务端。渲染层（NodeShell）
// 有覆盖条目时用它，否则用节点自身坐标。

/** 覆盖条目的存活时间。流断了（对方掉线/切走）就回落到持久化坐标，不能让节点
 *  永远停在最后一帧的中间位置。2s ≈ 10 个节流窗，正常拖拽期间不会误过期。 */
export const TRANSIENT_POSITION_TTL_MS = 2_000;

/** 收到权威几何后的瞬态抑制窗。2 倍节流窗（200ms）——比它短挡不住乱序到达的
 *  那一两帧，比它长会让「权威定格后队友立刻又拖起来」的第一帧被吞掉。 */
export const TRANSIENT_SUPPRESS_MS = 400;

const TRANSIENT_SWEEP_MS = 500;

export interface TransientPosition {
  x: number;
  y: number;
  expireAt: number;
}

// 抑制窗登记簿放模块级而不是 state：它不参与渲染，进 state 只会让每条权威 op
// 多触发一次无意义的订阅通知。
const transientSuppressUntil = new Map<string, number>();

let transientSweepTimer: ReturnType<typeof setTimeout> | undefined;

// 到点清理过期覆盖条目。必须真的 set 一次 state（而不是只在读取时判过期）——
// 组件靠订阅重渲染，不 set 的话节点会一直画在过期坐标上，直到下一次别的更新。
function scheduleTransientSweep(): void {
  if (transientSweepTimer !== undefined) return;
  transientSweepTimer = setTimeout(() => {
    transientSweepTimer = undefined;
    const now = Date.now();
    const current = useCanvasStore.getState().transientPositions;
    const kept: Record<string, TransientPosition> = {};
    let dropped = false;
    for (const [id, position] of Object.entries(current)) {
      if (position.expireAt > now) kept[id] = position;
      else dropped = true;
    }
    if (dropped) useCanvasStore.setState({ transientPositions: kept });
    if (Object.keys(kept).length > 0) scheduleTransientSweep();
  }, TRANSIENT_SWEEP_MS);
}

/**
 * 权威几何落地时清掉该节点的覆盖条目并开启抑制窗。**权威永远压过瞬态**：
 * 队友松手后的 patchPositions 是最终位置，此后再到的旧瞬态帧属于反序噪声。
 */
function settleTransientPosition(nodeId: string): void {
  transientSuppressUntil.set(nodeId, Date.now() + TRANSIENT_SUPPRESS_MS);
  const current = useCanvasStore.getState().transientPositions;
  if (!(nodeId in current)) return;
  const next = { ...current };
  delete next[nodeId];
  useCanvasStore.setState({ transientPositions: next });
}

/** 清空覆盖层与抑制窗。整快照重载/离开画布时调用——覆盖层描述的是「此刻谁在
 *  拖」，跨项目留着就是把上一张画布的中间坐标画到新画布的同 id 节点上。 */
export function clearTransientPositions(): void {
  transientSuppressUntil.clear();
  if (transientSweepTimer !== undefined) {
    clearTimeout(transientSweepTimer);
    transientSweepTimer = undefined;
  }
  if (Object.keys(useCanvasStore.getState().transientPositions).length === 0) return;
  useCanvasStore.setState({ transientPositions: {} });
}

/** 渲染层取位置的唯一入口：有覆盖条目用覆盖坐标，否则用节点自身坐标。 */
export function resolveNodePosition(
  node: { x: number; y: number },
  transient: TransientPosition | undefined,
): { x: number; y: number } {
  return transient ? { x: transient.x, y: transient.y } : { x: node.x, y: node.y };
}

// 远端数据进 store 前一律脱钩帧对象：帧由传输层持有，可能被复用/回收，
// 存进来的引用一旦被外部改写，画布状态就会隔空变化。
function cloneRemoteValue<T>(value: T): T {
  return structuredClone(value);
}

function geometryPatchRecord(geometry: RemoteNodeGeometry): Record<string, unknown> {
  const patch: Record<string, unknown> = {};
  if (typeof geometry.x === 'number') patch.x = geometry.x;
  if (typeof geometry.y === 'number') patch.y = geometry.y;
  if (typeof geometry.width === 'number') patch.width = geometry.width;
  if (typeof geometry.height === 'number') patch.height = geometry.height;
  return patch;
}

// 交互结束（松手/失焦）→ 把该节点挂起的远端 op 落地。store 单向依赖登记簿，
// 靠这个 listener 拿通知，避免 registry 反向 import store。
onInteractionEnd((nodeId) => {
  const pending = deferredRemoteOps.get(nodeId);
  if (!pending) return;
  deferredRemoteOps.delete(nodeId);
  // 本端保护（strip / overlay）在 applyRemoteOp 里做，挂起 flush 与直接落地
  // 因此共用同一条路径——两处各写一份必然漏掉其中一条时序。
  useCanvasStore.getState().applyRemoteOp(
    pending.node
      ? { kind: 'upsertNode', node: pending.node }
      : { kind: 'patchNode', nodeId, patch: pending.patch ?? {} },
  );
});

function sameStructuralNode(a: CanvasNode, b: CanvasNode): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
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
  const settings = node.settings
    ? (JSON.parse(JSON.stringify(node.settings)) as Record<string, unknown>)
    : undefined;
  if (settings) delete settings.pendingGen;

  const clone: CanvasNode = { ...rest, settings };
  if (clone.status === 'generating' || clone.status === 'uploading') {
    clone.status = clone.content ? 'succeeded' : 'idle';
    clone.progress = undefined;
  }
  return clone;
}

function cloneNodeForDuplicate(node: CanvasNode, id: string): CanvasNode {
  return { ...sanitizeNodeCopy(node), id, x: node.x + 32, y: node.y + 32 };
}

// 把 before→after 的节点/连线集合差量翻译成一串结构性 op。undo/redo 会整体
// 换掉画布状态，无法预知改了哪些，只能对比前后集合逐个补 upsert/delete。
// 发射顺序（先删节点、再 upsert 节点、再删连线、再 upsert 连线）保证队列串行
// 执行时连线两端节点已存在。
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
    if (!prev || !sameStructuralNode(prev, node)) emitCanvasOp({ kind: 'upsertNode', node });
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
  /** 瞬态坐标覆盖层（nodeId → 坐标 + 过期时刻）。与 nodes 平行的一层，**永不
   *  合并进 nodes**，见 applyTransientPositions 与 resolveNodePosition。 */
  transientPositions: Record<string, TransientPosition>;
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
   * `preserveUiState` 用于**同一张画布**的整拉兜底（团队并发编辑的 version 缺口
   * 自愈）——那不是换画布，是把本地对齐到服务端最新状态。清掉选中会把用户正开着
   * 的参数面板收起来、清掉 undo 栈会让他刚做的几步撤不回去，而这两样跟「服务端
   * 有新数据」毫无关系。选中集会按新节点列表剪枝（队友删掉的节点不能还留在选中里）。
   *
   * undo 栈跨整拉保留是有意的，且与 applyRemoteOp 的既有语义一致：远端改动本来
   * 就不进 undo 栈，撤销撤的一直是「我自己做过的那几步」。
   */
  load: (snapshot: Partial<CanvasSnapshot>, options?: { preserveUiState?: boolean }) => void;
  /** 合并一条**远端**（队友经服务端广播回来的）op。见 RemoteCanvasOp 的说明。 */
  applyRemoteOp: (op: RemoteCanvasOp) => void;
  /** 落地一批**瞬态**坐标（队友拖拽途中的实时位置）。见实现处的三条不变量。 */
  applyTransientPositions: (positions: Array<{ id: string; x: number; y: number }>) => void;
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

export const useCanvasStore = create<CanvasState>((set, get) => ({
  nodes: [],
  connections: [],
  transientPositions: {},
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
    // 整快照重载已经包含了所有远端改动，挂起的旧 op 再落一遍就是拿旧帧覆盖新状态。
    clearDeferredRemoteOps();
    // 覆盖层同理：快照里的坐标是权威的，留着旧覆盖条目会让节点继续画在别人
    // 上一轮拖到一半的位置上（切项目时甚至画到同 id 的别的节点上）。
    clearTransientPositions();
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
    // 保留模式：选中集按新节点剪枝——队友删掉的节点若还留在 selectedIds 里，
    // 后续任何「对选中批量操作」都会带上一个已经不存在的 id。
    const alive = new Set(nodes.map((n) => n.id));
    const selectedIds = new Set(
      [...useCanvasStore.getState().selectedIds].filter((id) => alive.has(id)),
    );
    set({ ...base, selectedIds });
  },

  // 远端 op 合并。三条不变量：
  //   1. 全程不调 emitCanvasOp——它是持久化 sink 的入口，回声出去就是「收到队友
  //      的改动 → 又当成自己的改动写回服务端 → 再广播给所有人」的风暴。
  //   2. 不 commit()（远端改动不进本地 undo 栈：Ctrl+Z 撤销队友的操作没有意义）、
  //      不清 selectedIds（清了会把本地正开着的参数面板收起来）。
  //   3. 不看 readOnly：服务端状态是权威，观看者也必须看到最新画布。
  applyRemoteOp: (op) => {
    switch (op.kind) {
      case 'upsertNode': {
        // 与帧解引用：op 的数据来自 WS 帧（或挂起队列），直接放进 store 的话
        // 上层再复用同一份对象就会隔空改到画布状态。
        const incoming = cloneRemoteValue(op.node);
        if (isNodeInteracting(incoming.id)) {
          deferRemoteNode(incoming.id, incoming, null);
          return;
        }
        // 本端仍占着的字段（pending + 在飞）盖回 store：整节点替换会把它们抹掉，
        // 而它们在服务端的落地序更晚，抹掉就是本地回退 + store≠server。
        const landed = overlayLocalPending(incoming.id, incoming);
        set((s) => {
          const index = s.nodes.findIndex((n) => n.id === incoming.id);
          if (index < 0) return { nodes: [...s.nodes, landed] };
          const nodes = [...s.nodes];
          nodes[index] = landed;
          return { nodes };
        });
        // 整节点必然带 x/y = 一次权威几何：清掉覆盖条目并开抑制窗，
        // 免得反序到达的旧瞬态又把它拖回中间态。
        settleTransientPosition(incoming.id);
        // 回吐**服务端这一步动作**本身，而不是 store 里合并完的节点——见
        // RemoteNodeStateOp 的说明。
        remoteNodeStateSink?.(incoming.id, { kind: 'node', node: incoming });
        return;
      }
      case 'patchNode': {
        const incomingPatch = cloneRemoteValue(op.patch);
        if (isNodeInteracting(op.nodeId)) {
          deferRemoteNode(op.nodeId, null, incomingPatch);
          return;
        }
        const current = get().nodes.find((n) => n.id === op.nodeId);
        // 服务端 PatchNode 对不存在的节点返回 ErrNodeNotFound；本地同样静默忽略
        // （节点可能刚被本端删掉，删除 op 与 patch op 的到达顺序不保证）。
        if (!current) return;
        const patch = stripAgainstLocal(op.nodeId, incomingPatch);
        if (!patch) return;
        const merged = applyMergePatch(current, patch) as CanvasNode;
        set((s) => ({ nodes: s.nodes.map((n) => (n.id === op.nodeId ? merged : n)) }));
        // 只有真的改了坐标才算权威几何——改提示词的 patch 不该把队友正在进行的
        // 拖拽流打断。
        if ('x' in patch || 'y' in patch) settleTransientPosition(op.nodeId);
        remoteNodeStateSink?.(op.nodeId, { kind: 'patch', patch });
        return;
      }
      case 'deleteNodes': {
        const ids = new Set(op.nodeIds);
        if (ids.size === 0) return;
        // 删除不挂起：节点在服务端已经没了，留着让用户接着拖只会拖一个幽灵。
        // 该节点上挂起的 op 一并作废。
        for (const id of ids) deferredRemoteOps.delete(id);
        set((s) => {
          const selected = [...s.selectedIds].filter((id) => !ids.has(id));
          return {
            nodes: s.nodes.filter((n) => !ids.has(n.id)),
            // 与后端 DeleteNodes 一致：连带删掉两端指向被删节点的连线。
            connections: s.connections.filter((c) => !ids.has(c.from) && !ids.has(c.to)),
            selectedIds: selected.length === s.selectedIds.size ? s.selectedIds : new Set(selected),
          };
        });
        // 节点没了，它的覆盖条目也就没了（留着只会占内存，且同 id 节点再出现时
        // 会被一个来路不明的坐标钉住）。
        for (const id of ids) settleTransientPosition(id);
        for (const id of ids) remoteNodeStateSink?.(id, null);
        return;
      }
      case 'patchPositions': {
        const applied = new Map<string, Record<string, unknown>>();
        for (const geometry of op.positions) {
          const patch = geometryPatchRecord(geometry);
          if (Object.keys(patch).length === 0) continue;
          // 拖拽中的节点如果直接吃下远端坐标，节点会在用户手底下跳走——挂起到松手。
          if (isNodeInteracting(geometry.id)) {
            deferRemoteNode(geometry.id, null, patch);
            continue;
          }
          // 松手瞬间：交互登记已解除，但落点的 PATCH 还在飞——此时远端坐标直接
          // 落地会把落点顶掉，还会被随后的 persist 当成我的移动写回服务端。
          const kept = stripAgainstLocal(geometry.id, patch);
          if (!kept) continue;
          applied.set(geometry.id, kept);
        }
        if (applied.size === 0) return;
        set((s) => ({
          nodes: s.nodes.map((n) => {
            const patch = applied.get(n.id);
            return patch ? ({ ...n, ...patch } as CanvasNode) : n;
          }),
        }));
        // 这是拖拽松手的权威定格：清覆盖条目 + 开抑制窗，反序到达的旧瞬态
        // 从此盖不住它（对方已松手，不会再有新瞬态来纠正）。
        for (const id of applied.keys()) settleTransientPosition(id);
        for (const [id, patch] of applied) remoteNodeStateSink?.(id, { kind: 'patch', patch });
        return;
      }
      case 'upsertConnection': {
        const incoming = cloneRemoteValue(op.connection);
        set((s) => {
          const index = s.connections.findIndex((c) => c.id === incoming.id);
          if (index < 0) return { connections: [...s.connections, incoming] };
          const connections = [...s.connections];
          connections[index] = incoming;
          return { connections };
        });
        return;
      }
      case 'deleteConnection': {
        set((s) => ({ connections: s.connections.filter((c) => c.id !== op.connectionId) }));
        return;
      }
    }
  },

  // 瞬态坐标落地。它是「队友的手正拖到这里」的实时投影，不是画布状态的一次变更。
  //
  // **只写覆盖层，绝不写 nodes[].x/y**——这是「瞬态永不落库」的结构性保证，不是
  // 靠调用方自觉。nodes[] 是 persistNodeById 的 diff 面：一旦把队友拖到一半的
  // 坐标写进去，本端之后任何一次持久化（改个提示词都算）都会把那个中间坐标当成
  // 自己的改动 PATCH 回服务端——队友松手后的真实位置被我覆盖，而且这次覆盖不会
  // 被任何广播纠正（自己发的 op 回来时按 source_mark 跳过）。覆盖层在 store 里
  // 与 nodes 平行，diff 结构性够不到它。
  //
  // 另外三条：不 commit()（不进 undo 栈）、不回吐 remoteNodeStateSink（不动基线）、
  // 本地在途的节点直接**忽略**而不是挂起（瞬态 200ms 一帧，挂起等于把必然过时的
  // 坐标留到松手后再弹一下）。
  applyTransientPositions: (positions) => {
    const now = Date.now();
    const updates = new Map<string, { x: number; y: number }>();
    for (const position of positions) {
      if (!position || typeof position.id !== 'string') continue;
      if (typeof position.x !== 'number' || typeof position.y !== 'number') continue;
      if (isNodeInteracting(position.id)) continue;
      // 刚落过权威几何的节点在抑制窗内不吃瞬态：网络乱序时「队友松手前的最后
      // 一帧瞬态」会晚于权威 patchPositions 到达，落下去就是把定格位置又拖回
      // 中间态，而且没有下一帧来纠正（对方已经松手，不再发瞬态）。
      const suppressedUntil = transientSuppressUntil.get(position.id);
      if (suppressedUntil !== undefined && suppressedUntil > now) continue;
      updates.set(position.id, { x: position.x, y: position.y });
    }
    if (updates.size === 0) return;
    const expireAt = now + TRANSIENT_POSITION_TTL_MS;
    set((s) => {
      const next = { ...s.transientPositions };
      for (const [id, update] of updates) next[id] = { ...update, expireAt };
      return { transientPositions: next };
    });
    scheduleTransientSweep();
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
    // 本端删掉的节点，其挂起的远端 op 一并作废——不然交互结束时那条挂起的
    // upsert 会把已删节点凭空复活。
    for (const id of ids) deferredRemoteOps.delete(id);
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

  undo: () => {
    const before = get();
    if (before.readOnly || before.undoStack.length === 0) return;
    const prev = before.undoStack[before.undoStack.length - 1];
    set((s) => ({
      nodes: prev.nodes,
      connections: prev.connections,
      view: prev.view,
      undoStack: s.undoStack.slice(0, -1),
      redoStack: [
        ...s.redoStack,
        { nodes: s.nodes, connections: s.connections, view: s.view },
      ],
    }));
    emitReconcile(before, prev);
  },

  redo: () => {
    const before = get();
    if (before.readOnly || before.redoStack.length === 0) return;
    const next = before.redoStack[before.redoStack.length - 1];
    set((s) => ({
      nodes: next.nodes,
      connections: next.connections,
      view: next.view,
      redoStack: s.redoStack.slice(0, -1),
      undoStack: [
        ...s.undoStack,
        { nodes: s.nodes, connections: s.connections, view: s.view },
      ],
    }));
    emitReconcile(before, next);
  },

  getSnapshot: () => ({
    nodes: get().nodes,
    connections: get().connections,
    view: get().view,
  }),

  getConnectedInputs: (nodeId) => {
    const state = get();
    const incoming = state.connections.filter((c) => c.to === nodeId);
    const images: string[] = [];
    const texts: string[] = [];
    const videos: string[] = [];
    const audios: string[] = [];
    const refs: ConnectedMediaRef[] = [];
    for (const conn of incoming) {
      const src = state.nodes.find((n) => n.id === conn.from);
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
  },
}));
