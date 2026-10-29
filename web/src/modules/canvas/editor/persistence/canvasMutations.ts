import type {
  CanvasMutationConnection,
  CanvasMutationNode,
  CanvasMutationResponse,
  CanvasNodeGeometryPatch,
} from '@/shared/api';
import {
  deleteCanvasConnection,
  deleteCanvasNodes,
  isCanvasOwnerLostError,
  patchCanvasNode,
  patchCanvasNodePositions,
  upsertCanvasConnection,
  upsertCanvasNode,
} from '@/shared/api';
import { applyMergePatch } from '../mergePatch';
import { cloneWithoutOversizedDataUrls, hasOversizedDataUrl, nodeForServer } from './payloadHygiene';
import { setCanvasOpSink, useCanvasStore, type CanvasOp } from '../store';
import type { CanvasNode } from '../types';

const nodePatchDebounceMs = 1500;

export type PatchNodeFn = (
  projectID: number,
  ownerToken: string,
  nodeID: string,
  patch: Record<string, unknown>,
) => Promise<CanvasMutationResponse>;

export type UpsertNodeFn = (
  projectID: number,
  ownerToken: string,
  node: CanvasMutationNode,
) => Promise<CanvasMutationResponse>;

interface CanvasPersistenceConfig {
  projectID: number | null;
  onVersion?: (version: number) => void;
  onError?: (err: unknown) => void;
  patchNode?: PatchNodeFn;
  upsertNode?: UpsertNodeFn;
}

interface MutationContext {
  projectID: number | null;
  ownerToken: string;
  generation: number;
  onVersion?: (version: number) => void;
}

interface PendingNodePatch extends MutationContext {
  patch: Record<string, unknown>;
  patchNode: PatchNodeFn;
  onError?: (err: unknown) => void;
}

let ownerToken = '';
let queue: Promise<void> = Promise.resolve();

// 外部变更整拉（externalChanges.ts）要知道本地是否还有没落定的写入：
// inflight 是已入队未完成的 mutation 数；mutationSeq 每次本地发起写入（入队或排 debounce patch）都 +1，
// 用来判断"拉快照期间本地有没有新写入"——有就丢弃这次拉到的结果，等空闲后重拉。
let inflight = 0;
let mutationSeq = 0;

export function isCanvasPersistenceIdle(): boolean {
  return inflight === 0 && pendingNodePatches.size === 0;
}

export function canvasMutationSeq(): number {
  return mutationSeq;
}
let config: CanvasPersistenceConfig = { projectID: null };
let ownerLostListeners = new Set<(err: unknown) => void>();
let pendingNodePatches = new Map<string, PendingNodePatch>();
let nodePatchTimers = new Map<string, ReturnType<typeof setTimeout>>();
let lifecycleGeneration = 0;

// per-node「上次已知的服务端节点状态」。persistNodeById 拿它做 diff，只把变更
// 字段发出去（PatchNode 递归合并），而不是整节点覆盖。
//
// 为什么必须有它：整节点 upsert 等于把内存里整份状态都当作要写入的内容，
// 没改的字段也会被原样重发。基线记录服务端状态，只发变化的字段：改哪个字段，
// 才写哪个字段。
//
// 谁推进基线（两个入口，都代表「服务端此刻就是这个状态」）：
//   1. 整快照加载后 seedCanvasBaselines()；
//   2. 本端 mutation 发送成功（upsert 发的是整节点，patch 按同一套递归合并规则并进去）。
let nodeBaselines = new Map<string, CanvasNode>();

export function configureCanvasPersistence(next: {
  projectID: number;
  onVersion?: (version: number) => void;
  onError?: (err: unknown) => void;
  patchNode?: PatchNodeFn;
  upsertNode?: UpsertNodeFn;
}): void {
  if (config.projectID !== null && config.projectID !== next.projectID) {
    // 时序守卫（换项目才可能走到）。resetCanvasPersistence 会**清空基线与
    // ownerToken**，而页面的调用顺序是「加载快照 → seedCanvasBaselines →
    // setCanvasOwnerToken → configureCanvasPersistence」——如果换项目时才在这里
    // 兜底 reset，刚为**新**项目播下的基线和刚设好的 token 会被一起清掉：
    // 基线没了 → 字段级 LWW 静默退化成整节点 upsert（并发编辑要消灭的互踩面又回来了）；
    // token 没了 → store 的结构性 op 被 applyCanvasOp 静默丢弃，用户的增删节点
    // 一个都不落库。两种都无报错、无 UI 反馈，只表现为「协作偶尔互相覆盖」。
    //
    // 今天不可达：所有 `/canvas/:id` 入口都来自会卸载本页的其它页面（卸载时
    // 已经 reset 过，config.projectID 回到 null）。所以这里不改行为、只把
    // 「悄悄退化」变成「控制台喊一声」——真出现同组件切项目时，正确修法是让
    // 调用方在 load 新快照**之前**显式 resetCanvasPersistence()。
    if (nodeBaselines.size > 0 || ownerToken) {
      console.warn(
        '[canvas] 切项目时 configureCanvasPersistence 清掉了已播种的基线/ownerToken：' +
          '调用方必须在加载新快照之前先 resetCanvasPersistence()，否则字段级 LWW 与结构性 op 会静默失效',
      );
    }
    resetCanvasPersistence();
  }
  config = next;
  // 注册 store 结构性 op 的持久化 sink：store 里任何节点/连线的增删都会经此
  // 翻译成一次局部 mutation 入队。放在 config 赋值之后，确保 applyCanvasOp 拿到
  // 的是最新 projectID。
  setCanvasOpSink(applyCanvasOp);
}

/**
 * 用整快照的节点播种基线。**只能传服务端来的快照**（首次加载、版本缺口整拉），
 * 不能拿本地构造的节点（例如本地新建、尚未落库的节点）来播——那些节点
 * 服务端根本没有，一旦有了基线，后续就会对不存在的节点发 PatchNode 而报错。
 * 没播种也不会坏：没基线 = 走整节点 upsert（即重构前的行为）。
 */
export function seedCanvasBaselines(nodes: CanvasNode[]): void {
  nodeBaselines = new Map(nodes.map((node) => [node.id, cloneNode(node)]));
}

// 把 store 广播的结构性 op 翻译成局部 mutation，走统一的串行队列。owner-token
// 或 projectID 缺失（尚未接管/已离开）时静默丢弃，等同只读。
export function applyCanvasOp(op: CanvasOp): void {
  const projectID = config.projectID;
  const token = ownerToken;
  if (projectID == null || !token) return;
  switch (op.kind) {
    case 'upsertNode':
      sendNodeUpsert(op.node);
      break;
    case 'patchNode':
      // undo/redo 里前后都存在的节点：与 persistNodeById 同一套「对基线 diff」。
      persistNodeState(op.node);
      break;
    case 'deleteNodes':
      // 节点没了，基线也就没了（同 id 的节点将来若再出现，从 upsert 重新建基线）。
      for (const id of op.ids) nodeBaselines.delete(id);
      void enqueueCanvasMutation(() => deleteCanvasNodes(projectID, token, op.ids));
      break;
    case 'upsertConnection':
      void enqueueCanvasMutation(() => upsertCanvasConnection(projectID, token, op.connection as unknown as CanvasMutationConnection));
      break;
    case 'deleteConnection':
      void enqueueCanvasMutation(() => deleteCanvasConnection(projectID, token, op.id));
      break;
  }
}

/**
 * 按 id 持久化节点当前状态。用于「先建空节点、异步补内容」的流程（拖入上传、
 * 素材库、历史投放）：addNode 已 upsert 了空壳，内容/assetId 落定后再调一次把
 * 变更写回。updateNode 不自动持久化（它也被生成回写复用），所以这类内容补齐
 * 必须显式触发。
 *
 * 100+ 个调用点看到的签名不变，内部从「整节点 upsert」换成了「对基线做 diff →
 * 只发变更字段的 PatchNode」：
 *   - 有基线且有变更 → PatchNode(变更字段)。只发变化的字段，改 A 字段不会重发
 *     没变的 B 字段。
 *   - 有基线且无变更 → 一个请求都不发（旧实现每次都发一整份）。
 *   - 相对基线**少了字段**（生成结束清掉 taskId / settings.pendingGen 之类）
 *     → 该字段发 `null`，按 RFC 7386 的删键语义走同一条 PatchNode。
 *   - 无基线（本地新建、或没播种过快照） → UpsertNode 整节点，成功后建立基线。
 */
export function persistNodeById(nodeID: string): void {
  const node = useCanvasStore.getState().nodesById.get(nodeID);
  if (!node) return;
  persistNodeState(node);
}

function persistNodeState(node: CanvasNode): void {
  const baseline = nodeBaselines.get(node.id);
  if (!baseline) {
    sendNodeUpsert(node);
    return;
  }
  // 与 upsert 同一份「发往服务端的形态」做 diff：抽帧这类本机字段不会被算成变更；
  // 基线里还留着存量快照带上来的帧时，会算出 null 把服务端那份删掉（P8）。
  const diff = diffNodeAgainstBaseline(baseline, nodeForServer(node));
  if (diff.kind === 'none') return;
  sendNodePatch(node.id, diff.patch);
}

// 整节点 upsert：发出去的那一份就是服务端随后的状态，成功即成为新基线。
function sendNodeUpsert(node: CanvasNode): void {
  const context = captureMutationContext();
  const projectID = context.projectID;
  const token = context.ownerToken;
  if (projectID == null || !token) return;
  // nodeForServer 已经是深拷贝：去掉本机抽帧字段、拒收超大 dataURL（P8）。
  const sent = nodeForServer(node);
  const upsert = config.upsertNode ?? upsertCanvasNode;
  void runNodeMutation(context, async () => {
    const result = await upsert(projectID, token, sent as unknown as CanvasMutationNode);
    // 生命周期校验：切项目/失权后回来的迟到响应不能污染新画布的基线。
    if (isMutationContextCurrent(context)) nodeBaselines.set(sent.id, sent);
    return result;
  });
}

// 字段级 patch：成功后按与服务端相同的递归合并规则把 patch 并进基线。
function sendNodePatch(nodeID: string, patch: Record<string, unknown>): void {
  const context = captureMutationContext();
  const projectID = context.projectID;
  const token = context.ownerToken;
  if (projectID == null || !token) return;
  const patchNode = config.patchNode ?? patchCanvasNode;
  void runNodeMutation(context, async () => {
    const result = await patchNode(projectID, token, nodeID, patch);
    if (isMutationContextCurrent(context)) mergePatchIntoBaseline(nodeID, patch);
    return result;
  });
}

/**
 * 落库一批节点几何（拖拽松手 / 自动排布）。走这里而不是直接
 * `enqueueCanvasMutation(patchCanvasNodePositions(...))`：成功后要推进基线——几何是
 * 节点字段，不推的话随后的 persistNodeById 会把 x/y/w/h 当成变更再发一遍。
 */
export function persistNodePositions(
  projectID: number,
  token: string,
  patches: CanvasNodeGeometryPatch[],
): void {
  if (patches.length === 0) return;
  const context = captureMutationContext();
  void runNodeMutation(context, async () => {
    const result = await patchCanvasNodePositions(projectID, token, patches);
    if (isMutationContextCurrent(context)) {
      for (const patch of patches) mergePatchIntoBaseline(patch.id, geometryRecord(patch));
    }
    return result;
  });
}

function geometryRecord(patch: CanvasNodeGeometryPatch): Record<string, unknown> {
  const record: Record<string, unknown> = {};
  if (typeof patch.x === 'number') record.x = patch.x;
  if (typeof patch.y === 'number') record.y = patch.y;
  if (typeof patch.width === 'number') record.width = patch.width;
  if (typeof patch.height === 'number') record.height = patch.height;
  return record;
}

// 节点 mutation 的统一出口：入队 + 把失败交给 onError。
// 为什么要显式 catch：这两条路都是 fire-and-forget（调用方是 persistNodeById /
// store op sink，没人 await），不接住的话一次 PatchNode 失败就是一条
// unhandled rejection——与 scheduleNodePatch 的定时 flush 同一套处理。
function runNodeMutation(
  context: MutationContext,
  work: () => Promise<CanvasMutationResponse | void>,
): Promise<void> {
  const onError = config.onError;
  return enqueueCanvasMutationInContext(work, context).catch((err: unknown) => {
    if (!isMutationContextCurrent(context)) return;
    try {
      onError?.(err);
    } catch {
      // 回调自身失败不再上抛：这里已经没有调用方能接住了。
    }
  });
}

function mergePatchIntoBaseline(nodeID: string, patch: Record<string, unknown>): void {
  const baseline = nodeBaselines.get(nodeID);
  if (!baseline) return;
  // 用与服务端同一套应用语义（RFC 7386，null=删键；见 ../mergePatch）——基线要
  // 复刻服务端算出来的结果，用别的规则合并就会两边分叉。
  const merged = applyMergePatch(baseline, patch) as CanvasNode;
  // 再 clone 一次：patch 里的数组/对象是**从 store 节点上摘下来的引用**，直接存进
  // 基线的话，组件将来就地改写这些结构会连基线一起改掉，diff 就永远算不出变化。
  nodeBaselines.set(nodeID, cloneNode(merged));
}

type NodeDiff =
  | { kind: 'none' }
  | { kind: 'patch'; patch: Record<string, unknown> };

/**
 * 节点相对基线的差量。语义与 patch 的**应用**规则（`../mergePatch` 的
 * `applyMergePatch`，后端对侧是 `server/internal/canvasstate/state.go` 的
 * `mergeRawValue`）严格对称——RFC 7386 JSON Merge Patch：
 *   - 普通对象递归比到叶子标量，只发真正变了的那条路径（发最小 patch）；
 *   - 数组和非普通对象（Date 等）整体比较、整体发送——后端对数组就是整体替换，
 *     这边做数组 diff 两边对不上；
 *   - 值为 `undefined` 等同「没有这个字段」（JSON 序列化本来就会丢掉它）；
 *   - **基线有、现在没有 → 发 `null`（删键）**。生成路径每跑完一次就要清掉
 *     taskId / settings.pendingGen 这类阶段字段（十余处），退回整节点 upsert
 *     的话，最热的一条路径反而永远在整覆盖，字段级 LWW 白做。
 *   - `id` 不进 patch：它是 URL 上的定位参数，不是待改字段（与后端对称）。
 */
function diffNodeAgainstBaseline(baseline: CanvasNode, node: CanvasNode): NodeDiff {
  const patch = diffRecords(
    baseline as unknown as Record<string, unknown>,
    node as unknown as Record<string, unknown>,
  );
  delete patch.id;
  if (Object.keys(patch).length === 0) return { kind: 'none' };
  return { kind: 'patch', patch };
}

function diffRecords(
  baseline: Record<string, unknown>,
  next: Record<string, unknown>,
): Record<string, unknown> {
  const patch: Record<string, unknown> = {};

  for (const [key, nextValue] of Object.entries(next)) {
    if (nextValue === undefined) continue;
    const baseValue = baseline[key];
    if (baseValue === undefined) {
      patch[key] = nextValue;
      continue;
    }
    if (isPlainObject(baseValue) && isPlainObject(nextValue)) {
      const nested = diffRecords(baseValue, nextValue);
      if (Object.keys(nested).length > 0) patch[key] = nested;
      continue;
    }
    if (!jsonEqual(baseValue, nextValue)) patch[key] = nextValue;
  }

  for (const [key, baseValue] of Object.entries(baseline)) {
    if (baseValue === undefined) continue;
    if (next[key] === undefined) patch[key] = null;
  }

  return patch;
}

function jsonEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  return JSON.stringify(a) === JSON.stringify(b);
}

// 基线必须与 store 里的节点对象完全脱钩：store 的 updateNode 走的是浅拷贝
// （`{...n, ...patch}`），settings 这类嵌套对象在两边共享引用，就地改写会让基线
// 悄悄跟着变，diff 永远算出「没变化」。
function cloneNode(node: CanvasNode): CanvasNode {
  return JSON.parse(JSON.stringify(node)) as CanvasNode;
}

export function setCanvasOwnerToken(token: string): void {
  ownerToken = token;
}

export function getCanvasOwnerToken(): string {
  return ownerToken;
}

export function onCanvasOwnerLost(listener: (err: unknown) => void): () => void {
  ownerLostListeners.add(listener);
  return () => {
    ownerLostListeners.delete(listener);
  };
}

export function enqueueCanvasMutation(work: () => Promise<CanvasMutationResponse | void>): Promise<void> {
  return enqueueCanvasMutationInContext(work, captureMutationContext());
}

function enqueueCanvasMutationInContext(
  work: () => Promise<CanvasMutationResponse | void>,
  context: MutationContext,
): Promise<void> {
  const run = async () => {
    // 已经入队的 mutation 即使在 reset（页面卸载、sidecar 重启导致整棵树重挂）之后也照常
    // 发出：那是用户已经做完的修改，丢掉就是静默丢数据。sidecar 重启期间 axios 拦截器会
    // 等到新端口/新 token 再发。迟到响应只是不再推进基线、不再回调（下面各处仍按
    // isMutationContextCurrent 隔离），不会污染新会话。
    // 还没入队的（debounce 中的 patch）由 flushNodePatch 自己按上下文判定。
    // try/finally 放在 run 内部而不是外面再包一层 promise：多一层会多出微任务 tick，
    // 现有测试对 flush 的微任务顺序是敏感的。
    try {
      if (!context.ownerToken) return;
      try {
        const result = await work();
        if (typeof result?.version === 'number' && isMutationContextCurrent(context)) {
          context.onVersion?.(result.version);
        }
      } catch (err) {
        if (isCanvasOwnerLostError(err)) {
          if (isMutationContextCurrent(context)) {
            emitOwnerLost(err);
          }
          return;
        }
        throw err;
      }
    } finally {
      inflight = Math.max(0, inflight - 1);
    }
  };

  inflight += 1;
  mutationSeq += 1;
  const current = queue.then(run, run);
  queue = current.catch(() => undefined);
  return current;
}

export function scheduleNodePatch(nodeID: string, patch: Record<string, unknown>): void {
  mutationSeq += 1;
  const context = captureMutationContext();
  const existing = pendingNodePatches.get(nodeID);
  pendingNodePatches.set(nodeID, {
    ...context,
    patchNode: config.patchNode ?? patchCanvasNode,
    onError: config.onError,
    patch: existing && isSameMutationContext(existing, context)
      ? mergePlainObjectRecords(existing.patch, patch)
      : mergePlainObjectRecords(undefined, patch),
  });

  const existingTimer = nodePatchTimers.get(nodeID);
  if (existingTimer !== undefined) {
    clearTimeout(existingTimer);
  }

  nodePatchTimers.set(nodeID, setTimeout(() => {
    const scheduled = pendingNodePatches.get(nodeID);
    flushNodePatch(nodeID).catch((err: unknown) => {
      if (scheduled && isMutationContextCurrent(scheduled)) {
        try {
          scheduled.onError?.(err);
        } catch {
          // Scheduled flushes cannot surface callback failures to a caller.
        }
      }
    });
  }, nodePatchDebounceMs));
}

export async function flushNodePatch(nodeID: string): Promise<void> {
  const timer = nodePatchTimers.get(nodeID);
  if (timer !== undefined) {
    clearTimeout(timer);
    nodePatchTimers.delete(nodeID);
  }

  const pending = pendingNodePatches.get(nodeID);
  if (!pending) return;
  pendingNodePatches.delete(nodeID);

  if (!isMutationContextCurrent(pending)) return;
  // 调用方各自拼的 patch 也要过一遍：超大 dataURL 不进 payload（P8）。只在真的
  // 夹带时才 JSON 往返拷贝，平常原样发出（Date 等非普通值留给 axios 序列化）。
  const patch = hasOversizedDataUrl(pending.patch)
    ? { ...pending, patch: cloneWithoutOversizedDataUrls(pending.patch) }
    : pending;

  await enqueueCanvasMutationInContext(async () => {
    if (patch.projectID == null) {
      return Promise.resolve();
    }
    const result = await patch.patchNode(patch.projectID, patch.ownerToken, nodeID, patch.patch);
    // debounce 落库的 patch 同样推进基线：不推的话基线一直落后于服务端，
    // persistNodeById 之后会把这些字段的旧值再算成「变更」发一遍。
    if (isMutationContextCurrent(patch)) mergePatchIntoBaseline(nodeID, patch.patch);
    return result;
  }, patch);
}

export async function flushAllNodePatches(): Promise<void[]> {
  const results = await settleAllNodePatches();
  rethrowFirstRejected(results);
  return results.map(() => undefined);
}

export async function flushCanvasMutations(): Promise<void> {
  const patchResults = await settleAllNodePatches();
  await queue;
  rethrowFirstRejected(patchResults);
}

export function geometryPatchFromNode(node: {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
}): CanvasNodeGeometryPatch {
  return {
    id: node.id,
    x: node.x,
    y: node.y,
    width: node.width,
    height: node.height,
  };
}

/**
 * 画布页卸载时用：先把 debounce 中的 patch 全部 flush 进队列（不 await），再 reset。
 * 入队的任务不受 reset 影响，会照常发出——sidecar 自动重启时 SidecarGate 会卸掉整棵树，
 * 不先 flush 的话这些修改会被静默丢掉。
 */
export function retireCanvasPersistence(): void {
  // flushNodePatch 在第一个 await 之前就已同步入队，所以紧接着 reset 不会把它们作废。
  void settleAllNodePatches();
  resetCanvasPersistence();
}

/**
 * 等已入队的 mutation 全部落定（成功或失败都算），最多等 timeoutMs。重挂后的画布页
 * 先等它再拉快照，保证上一棵树留下的修改在新快照里看得到，不会被快照覆盖掉。
 * 超时就放行：宁可少等，也不能让页面一直卡在加载。
 */
export function waitForCanvasMutationsIdle(timeoutMs = 15_000): Promise<void> {
  const drained = queue.then(() => undefined, () => undefined);
  return new Promise<void>((resolve) => {
    const timer = setTimeout(resolve, timeoutMs);
    void drained.then(() => {
      clearTimeout(timer);
      resolve();
    });
  });
}

export function resetCanvasPersistence(): void {
  setCanvasOpSink(null);
  // 基线是「服务端某个项目的节点状态」，跨项目/跨会话残留会让新画布对不存在的
  // 节点发 patch。清空后即回到「无基线 → 整节点 upsert」的安全默认。
  nodeBaselines = new Map();
  ownerToken = '';
  // 不重置 queue：已入队的任务继续按序发出，新会话的 mutation 排在它们后面，顺序不乱。
  config = { projectID: null };
  lifecycleGeneration += 1;
  for (const timer of nodePatchTimers.values()) {
    clearTimeout(timer);
  }
  nodePatchTimers = new Map();
  pendingNodePatches = new Map();
  ownerLostListeners = new Set();
}

export function resetCanvasPersistenceForTest(): void {
  resetCanvasPersistence();
  // 测试之间彻底隔离：上一个用例故意挂起的任务不能拖住下一个用例的队列。
  queue = Promise.resolve();
  inflight = 0;
  mutationSeq = 0;
}

export function emitOwnerLostForTest(err: unknown): void {
  emitOwnerLost(err);
}

function emitOwnerLost(err: unknown): void {
  for (const listener of ownerLostListeners) {
    try {
      listener(err);
    } catch {
      // Listener failures should not block later listeners or reject mutation work.
    }
  }
}

function settleAllNodePatches(): Promise<PromiseSettledResult<void>[]> {
  return Promise.allSettled([...pendingNodePatches.keys()].map((nodeID) => flushNodePatch(nodeID)));
}

function rethrowFirstRejected(results: PromiseSettledResult<void>[]): void {
  const firstRejected = results.find((result): result is PromiseRejectedResult => (
    result.status === 'rejected'
  ));
  if (firstRejected) {
    throw firstRejected.reason;
  }
}

function captureMutationContext(): MutationContext {
  return {
    projectID: config.projectID,
    ownerToken,
    generation: lifecycleGeneration,
    onVersion: config.onVersion,
  };
}

function isMutationContextCurrent(context: MutationContext): boolean {
  return (
    context.generation === lifecycleGeneration
    && context.projectID === config.projectID
    && context.ownerToken === ownerToken
  );
}

function isSameMutationContext(a: MutationContext, b: MutationContext): boolean {
  return (
    a.generation === b.generation
    && a.projectID === b.projectID
    && a.ownerToken === b.ownerToken
  );
}

function mergePlainObjectRecords(
  current: Record<string, unknown> | undefined,
  patch: Record<string, unknown>,
): Record<string, unknown> {
  const merged: Record<string, unknown> = { ...(current ?? {}) };
  for (const [key, nextValue] of Object.entries(patch)) {
    const currentValue = merged[key];
    if (isPlainObject(currentValue) && isPlainObject(nextValue)) {
      merged[key] = mergePlainObjectRecords(currentValue, nextValue);
    } else {
      merged[key] = nextValue;
    }
  }
  return merged;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}
