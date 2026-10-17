// agent「画布操作」写指令的前端执行器。后端 tools_canvas_ops.go 的五个写工具
//（create/update/delete_node、connect_nodes/remove_connection）各 emit 一种
// *_info artifact，经 onAgentArtifact 分流到这里执行。
//
// 为什么写操作必须在前端执行：画布是「浏览器持编辑锁 + 局部 mutation 上推」
// 模型，服务端绕过浏览器直写 DB，浏览器内存里的画布不知情，下一个用户操作
// 就会互相覆盖。这里全部走 store 现有动作，天然获得 UI 实时可见、undo 可
// 回退、既有持久化路三件事。
//
// 两条铁律（与后端红线成对）：
//   1. 不改 status / 不写机器管理字段 / 不触发任何生成——铺参数和点火永远是
//      两个动作（auto-memory feedback_batch_spawn_no_autosubmit 同源）；
//   2. 非法指令（找不到节点/未知类型/坏 payload）一律 console.warn + 静默
//      丢弃，绝不 throw——artifact 是流式管道，一条坏指令不该打断后续帧。
//
// 抽成纯函数（store/persistNode 都注入）是为了可测：CanvasEditorPage 的
// onAgentArtifact 只是把真实依赖填进来，本文件不 import canvasMutations→api 链。

import { DEFAULT_SIZES } from '../store';
import type { CanvasNode, Connection, NodeType } from '../types';
import { resetsDynamicFields, stripNonWritable } from './canvasManifest';
import { modelSettingDefaults } from '../utils/generationInputs';
import type { ModelUISchema } from '@/shared/api';

/** 画布操作类 artifact_type 清单。onAgentArtifact 用它分流：这 5 类不走
 *  「落新产物节点 + 排布」逻辑，全部进 applyCanvasOpArtifact。 */
export const CANVAS_OP_ARTIFACT_TYPES: ReadonlySet<string> = new Set([
  'node_create_info',
  'node_update_info',
  'node_delete_info',
  'connection_create_info',
  'connection_delete_info',
]);

export function isCanvasOpArtifact(artifactType?: string): boolean {
  return !!artifactType && CANVAS_OP_ARTIFACT_TYPES.has(artifactType);
}

// 字段权限现在由能力清单（canvasManifest）决定，不再是这里硬编码的三个名字。
// 后端按同一份清单校验过一遍（非 open 字段直接拒绝并回列可写清单），这里再剥
// 一次是纵深防御——两侧都不给这个口子，谁先漂移都兜得住。差别在于这里静默
// 剥离而不是报错：帧已经到了浏览器，没有回执通道能把错误递回 agent。

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** 解画布操作 payload。后端字段是 json.RawMessage：WS 帧 / 落库的 tool_event
 *  block 序列化后都是**内嵌 JSON 对象**（不是字符串化的 JSON）。字符串分支
 *  纯属防御——若哪层中转把它降级成了字符串，这里还能救回来。 */
export function parseCanvasOpPayload(payload: unknown): Record<string, unknown> | null {
  if (isPlainObject(payload)) return payload;
  if (typeof payload === 'string' && payload.trim() !== '') {
    try {
      const parsed: unknown = JSON.parse(payload);
      if (isPlainObject(parsed)) return parsed;
    } catch {
      // fallthrough：按坏 payload 处理
    }
  }
  return null;
}

function sanitizeSettings(nodeType: string, raw: unknown): Record<string, unknown> {
  if (!isPlainObject(raw)) return {};
  const { kept, stripped } = stripNonWritable(nodeType, raw);
  if (stripped.length > 0) {
    console.warn('[agent canvas-op] 按能力清单剥掉不可写字段', nodeType, stripped);
  }
  return kept;
}

/**
 * 切模型时把动态字段重置为新模型的默认值。
 *
 * 用户手动切模型时前端做的是
 *   updateNodeSettings(id, { modelId, ...modelSettingDefaults(m.uiSchema) })
 * ——写 modelId 的同时把比例/分辨率/时长重置为新模型的默认值。agent 只写
 * modelId 会把旧模型的分辨率档位留在新模型上，提交生成时非法或产出意外规格。
 *
 * 补齐放在执行侧而不是让 agent 自己拼：这里能直接复用 UI 用的同一个
 * modelSettingDefaults，跟手动操作走同一条代码路，不会漂移。agent 只需要说
 * 「换成 X 模型」，一致性不归它管。
 *
 * 只在 modelId **确实变了**时补。同值重复写（agent 复述当前状态）不该把用户
 * 手调过的分辨率打回默认。
 */
function withDynamicFieldReset(
  nodeType: string,
  settings: Record<string, unknown>,
  currentModelId: string | undefined,
  resolveUiSchema: ((modelId: string) => ModelUISchema | undefined) | undefined,
): Record<string, unknown> {
  const next = settings.modelId;
  if (typeof next !== 'string' || next === '' || next === currentModelId) return settings;
  if (!resetsDynamicFields(nodeType, 'modelId') || !resolveUiSchema) return settings;
  const uiSchema = resolveUiSchema(next);
  if (!uiSchema) return settings;
  // 默认值垫在前面：agent 若同时显式给了 ratio 等字段，它的值赢。
  return { ...modelSettingDefaults(uiSchema), ...settings };
}

/** 落到节点 settings.agentMeta 的产物溯源信息（与 onAgentArtifact 既有的
 *  text_info/gen_node 路径同一形态）——聊天气泡的定位 chip 按 artifactId 反查。 */
export interface AgentNodeMeta {
  threadId: string;
  actionId: string;
  artifactId: string;
  agentName: string;
}

/** 执行所需的 store 能力子集。结构化地对齐 useCanvasStore 的 state（直接传
 *  getState() 即可）；收窄接口是为了单测不用铺整个 CanvasState。 */
export interface CanvasOpStore {
  nodes: CanvasNode[];
  connections: Connection[];
  commit: () => void;
  addNode: (type: NodeType, worldPosition: { x: number; y: number }) => string;
  updateNode: (id: string, patch: Partial<CanvasNode>) => void;
  updateNodeSettings: (id: string, patch: Record<string, unknown>) => void;
  deleteNodes: (ids: string[]) => void;
  addConnection: (from: string, to: string, inputType?: string) => void;
  removeConnection: (id: string) => void;
}

// 创建节点时用来排序落点的名字。settings 是 agent 递来的自由 map，取名字这一
// 步要能扛住任意形状——拿不到就返回 undefined，落点退回「最小空位」。
function createNodeName(settings: unknown): string | undefined {
  if (!settings || typeof settings !== 'object') return undefined;
  const name = (settings as Record<string, unknown>).name;
  return typeof name === 'string' ? name : undefined;
}

export function applyCanvasOpArtifact(opts: {
  store: CanvasOpStore;
  artifact: { artifact_type?: string; artifact_id: string; name?: string; payload?: unknown };
  agentMeta: AgentNodeMeta;
  /** node_create_info 未带坐标时的落点排布（与其他 agent 产物共用同一套
   *  网格账本，按名字里的序号排位、互不重叠）。惰性调用——带显式坐标的创建
   *  不该消耗一个格子。name 传节点名，让它和 gen/text 产物排进同一套序号。 */
  nextDropPoint: (name?: string) => { x: number; y: number };
  /** 节点级持久化入口（persistNodeById）。注入而不是直接 import，见文件头。 */
  persistNode: (id: string) => void;
  /** 按 modelId 取模型的 uiSchema，用于切模型时重置动态字段。注入而不是这里
   *  自己发请求：模型目录是 react-query 缓存的（'runtime-models'），页面层
   *  已经有一份，重新拉一遍既慢又可能拿到不一致的版本。缺省则不做重置——
   *  退化成只写 modelId 的旧行为，不会因此失败。 */
  resolveUiSchema?: (modelId: string) => ModelUISchema | undefined;
}): void {
  const { store, artifact, agentMeta, nextDropPoint, persistNode, resolveUiSchema } = opts;
  const payload = parseCanvasOpPayload(artifact.payload);
  if (!payload) {
    console.warn('[agent canvas-op] payload 缺失或非法，丢弃', artifact.artifact_type, artifact.artifact_id);
    return;
  }

  switch (artifact.artifact_type) {
    case 'node_create_info': {
      const nodeType = payload.type;
      // 用 DEFAULT_SIZES 当前端节点类型清单的真源校验：addNode 对未知类型
      // 读不到默认尺寸会直接抛错。后端也有一份 knownNodeTypes 白名单，这里
      // 兜的是两侧清单漂移的窗口期。
      if (typeof nodeType !== 'string' || !(nodeType in DEFAULT_SIZES)) {
        console.warn('[agent canvas-op] 未知节点类型，丢弃', nodeType);
        return;
      }
      const hasX = typeof payload.x === 'number';
      const hasY = typeof payload.y === 'number';
      // addNode 把入参当**中心点**（内部减半宽半高），而 read_canvas 回显给
      // planner 的 x/y 是左上角。planner 指定的坐标必须按左上角语义执行——
      // 先 addNode 再 updateNode 把 x/y 钉回去，坐标系才和它读到的一致。
      const id = store.addNode(
        nodeType as NodeType,
        hasX && hasY
          ? { x: payload.x as number, y: payload.y as number }
          : nextDropPoint(createNodeName(payload.settings)),
      );
      if (!id) return; // readOnly 下 addNode 返回空串——与 store 其他写路径同语义
      const geo: Partial<CanvasNode> = {};
      if (hasX) geo.x = payload.x as number;
      if (hasY) geo.y = payload.y as number;
      if (Object.keys(geo).length > 0) store.updateNode(id, geo);
      // agentMeta 放在展开之后：payload.settings 里即便混进同名字段
      //（sanitize 已剥，但顺序上也让我方溯源信息永远赢）。
      // 新建节点没有「模型现值」，本次带了 modelId 就按它补一套默认参数——
      // 与用户在 UI 上新建节点后选模型的结果一致。
      const created = withDynamicFieldReset(
        nodeType,
        sanitizeSettings(nodeType, payload.settings),
        undefined,
        resolveUiSchema,
      );
      store.updateNodeSettings(id, { ...created, agentMeta });
      // addNode 只 upsert 了空壳，settings/几何落定后补一次完整节点——与
      // text_info / gen_node 落节点同一条持久化路。
      persistNode(id);
      return;
    }

    case 'node_update_info': {
      const nodeId = typeof payload.node_id === 'string' ? payload.node_id : '';
      if (!store.nodes.some((n) => n.id === nodeId)) {
        console.warn('[agent canvas-op] 目标节点不存在，丢弃 update', nodeId);
        return;
      }
      const target = store.nodes.find((n) => n.id === nodeId)!;
      const currentModelId = (target.settings as { modelId?: unknown } | undefined)?.modelId;
      const settings = withDynamicFieldReset(
        target.type,
        sanitizeSettings(target.type, payload.settings),
        typeof currentModelId === 'string' ? currentModelId : undefined,
        resolveUiSchema,
      );
      const geo: Partial<CanvasNode> = {};
      for (const key of ['x', 'y', 'width', 'height'] as const) {
        if (typeof payload[key] === 'number') geo[key] = payload[key] as number;
      }
      if (Object.keys(settings).length === 0 && Object.keys(geo).length === 0) return;
      // updateNode/updateNodeSettings 本身不进 undo 栈（它们也被生成回写和
      // 拖拽每帧调用）。agent 的修改是一次性远程指令，用户没有「输入过程」
      // 可感知，这里显式 commit 一次快照，让 Ctrl+Z 能一步回退整个操作——
      // 统一的安全兜底就是 undo 栈。
      store.commit();
      if (Object.keys(settings).length > 0) store.updateNodeSettings(nodeId, settings);
      if (Object.keys(geo).length > 0) store.updateNode(nodeId, geo);
      // 几何持久化：手动拖拽走 scheduleNodePatch 的 debounce 几何 patch，那是
      // 为「每帧改动合并」设计的；agent 是一次性全量意图，直接按「补一次完整
      // 节点」入口 upsert，settings 与几何一趟落库、无需 debounce。
      persistNode(nodeId);
      return;
    }

    case 'node_delete_info': {
      const nodeId = typeof payload.node_id === 'string' ? payload.node_id : '';
      if (!store.nodes.some((n) => n.id === nodeId)) {
        console.warn('[agent canvas-op] 目标节点不存在，丢弃 delete', nodeId);
        return;
      }
      // 与用户手动删除同一入口：连带清相关连线、undo 入栈、结构性 op 持久化
      // 全在 store.deleteNodes 内收口，这里不重复实现任何一步。
      store.deleteNodes([nodeId]);
      return;
    }

    case 'connection_create_info': {
      const from = typeof payload.from === 'string' ? payload.from : '';
      const to = typeof payload.to === 'string' ? payload.to : '';
      const present = new Set(store.nodes.map((n) => n.id));
      if (!from || !to || !present.has(from) || !present.has(to)) {
        // 两端节点必须都在——悬空连线会让 ConnectionLayer 画向不存在的锚点。
        console.warn('[agent canvas-op] 连线端点不存在，丢弃', from, to);
        return;
      }
      // 去重、不自连、undo 入栈、emitCanvasOp 持久化全部在 store.addConnection 内。
      store.addConnection(from, to);
      return;
    }

    case 'connection_delete_info': {
      const from = typeof payload.from === 'string' ? payload.from : '';
      const to = typeof payload.to === 'string' ? payload.to : '';
      // 同一对节点可能有多条不同 inputType 的连线（addConnection 按
      // from+to+inputType 去重）；agent 的语义是「断开这两个节点」，一并移除。
      const matches = store.connections.filter((c) => c.from === from && c.to === to);
      if (matches.length === 0) {
        console.warn('[agent canvas-op] 连线不存在，丢弃断开', from, to);
        return;
      }
      for (const conn of matches) store.removeConnection(conn.id);
      return;
    }

    default:
      // 分流守卫（isCanvasOpArtifact）之外的类型不该走到这里；防御性丢弃。
      console.warn('[agent canvas-op] 非画布操作类型', artifact.artifact_type);
  }
}
