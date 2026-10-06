// apps/web/src/canvas/workflowSerializer.ts
//
// Canvas <-> 后端 WorkflowStep[]（Skill JSON）的双向转换。
// 只被超级管理员的「导出为 skill JSON」/「从 skill JSON 导入」这两个 canvas
// 工具使用——从不和 API 通信，JSON 是通过一个复制粘贴框人工搬运的。
//
// 支持的节点（MVP）：
//   - gen-image / gen-video  → agent_tool 调用
//   - text-node / input-image / video-input / input-audio
//                            → workflow initialVars（不生成 step）
//   - preview / local-save / image-compare
//                            → sink，编译时丢弃
// 其他任何 NodeType 都会产生一条 warning 并被跳过。

import type { CanvasNode, CanvasSnapshot, Connection, NodeType } from './types';

// ──────────────────────────────────────────────────────────────────────
// 对外结构——和 apps/api/internal/agent/skills_repo.go::Skill 保持一致。
// 用 snake_case 的 key，这样 JSON 能直接复制粘贴进 DB row，不用再做转换。
// ──────────────────────────────────────────────────────────────────────

export interface WorkflowStep {
  name: string;
  tool_name: string;
  inputs: Record<string, unknown>;
}

export interface SkillExport {
  id?: string;
  name: string;
  description: string;
  category: string;
  icon_url?: string;
  skill_type: 'workflow';
  system_prompt_addon?: string;
  force_tools?: string[];
  param_defaults?: Record<string, unknown>;
  workflow_steps: WorkflowStep[];
  is_official?: boolean;
  is_published?: boolean;
  sort_weight?: number;
}

export interface CompileResult {
  skill: SkillExport;
  warnings: string[];
}

// ──────────────────────────────────────────────────────────────────────
// 节点分类
// ──────────────────────────────────────────────────────────────────────

const INPUT_NODE_TYPES: ReadonlySet<NodeType> = new Set([
  'input-image',
  'input-audio',
  'video-input',
  'text-node',
]);

const SINK_NODE_TYPES: ReadonlySet<NodeType> = new Set([
  'preview',
  'local-save',
  'image-compare',
]);

// node.type → agent_tool name。只映射目前 `agent_tools` 表里存在的工具。
// 其他 canvas 节点类型需要先注册各自的 agent_tool
// 条目，才能参与编译。
const NODE_TO_TOOL: Partial<Record<NodeType, string>> = {
  'gen-image': 'generate_image',
  'gen-video': 'generate_video',
};

// 解码用的反向映射。每个工具都解析回唯一的标准 canvas 节点类型——
// 模型相关的变体会以同一个 gen-image 节点来回转换，
// 具体模型通过 node settings 来选。
const TOOL_TO_NODE: Record<string, NodeType> = {
  generate_image: 'gen-image',
  generate_video: 'gen-video',
};

// ──────────────────────────────────────────────────────────────────────
// 编译：canvas snapshot → Skill JSON
// ──────────────────────────────────────────────────────────────────────

export function compileToSkill(
  snapshot: CanvasSnapshot,
  meta: { name: string; description: string; category?: string },
): CompileResult {
  const warnings: string[] = [];

  // 1) Kahn 拓扑排序——产出的顺序保证每个 node 都排在它入边来源的后面。
  const ordered = topologicalSort(snapshot.nodes, snapshot.connections, warnings);

  // 2) 建一张稳定的名字表。工具节点命名为 step_N_<type>，输入节点命名为
  //    input_<type>_<short_id>（以 {{input.X}} 形式被引用）。
  const nameById = new Map<string, string>();
  let toolCounter = 0;
  for (const node of ordered) {
    if (NODE_TO_TOOL[node.type]) {
      nameById.set(node.id, `step_${++toolCounter}_${slug(node.type)}`);
    } else if (INPUT_NODE_TYPES.has(node.type)) {
      nameById.set(node.id, `input_${slug(node.type)}_${node.id.slice(0, 6)}`);
    } else if (!SINK_NODE_TYPES.has(node.type)) {
      warnings.push(`未支持的节点类型: ${node.type}（id=${node.id}），已跳过`);
    }
  }

  // 3) 按拓扑顺序产出 step。
  const steps: WorkflowStep[] = [];
  for (const node of ordered) {
    const toolName = NODE_TO_TOOL[node.type];
    if (!toolName) continue;

    const stepName = nameById.get(node.id)!;
    const inputs: Record<string, unknown> = {};
    const s = (node.settings ?? {}) as Record<string, unknown>;
    if (typeof s.prompt === 'string') inputs.prompt = s.prompt;
    if (typeof s.ratio === 'string') inputs.ratio = s.ratio;
    if (typeof s.resolution === 'string') inputs.resolution = s.resolution;
    if (typeof s.duration === 'number') inputs.duration = s.duration;

    // 把入边解析成 image/video/audio 引用（或 text → prompt）。
    const refsByKind: Record<'image' | 'video' | 'audio', string[]> = {
      image: [],
      video: [],
      audio: [],
    };
    for (const conn of snapshot.connections.filter((c) => c.to === node.id)) {
      const fromName = nameById.get(conn.from);
      if (!fromName) {
        warnings.push(`step ${stepName} 上游 ${conn.from} 未注册（被跳过的节点类型）`);
        continue;
      }
      const fromNode = snapshot.nodes.find((n) => n.id === conn.from);
      if (!fromNode) continue;
      const isInput = INPUT_NODE_TYPES.has(fromNode.type);
      const placeholder = isInput
        ? `{{input.${fromName}}}`
        : `{{step.${fromName}.url}}`;
      const kind = conn.inputType ?? guessOutputKind(fromNode.type);
      if (kind === 'image') refsByKind.image.push(placeholder);
      else if (kind === 'video') refsByKind.video.push(placeholder);
      else if (kind === 'audio') refsByKind.audio.push(placeholder);
      else inputs.prompt = placeholder; // text → prompt
    }
    if (refsByKind.image.length > 0) inputs.images = refsByKind.image;
    if (refsByKind.video.length > 0) inputs.videos = refsByKind.video;
    if (refsByKind.audio.length > 0) inputs.audios = refsByKind.audio;

    steps.push({ name: stepName, tool_name: toolName, inputs });
  }

  // 4) 从输入节点收集 initialVars——param_defaults 预填后端预期的 key，
  //    这样管理员能抽查这个 skill 在调用时需要哪些参数。
  const paramDefaults: Record<string, unknown> = {};
  for (const node of snapshot.nodes) {
    if (INPUT_NODE_TYPES.has(node.type)) {
      const name = nameById.get(node.id);
      if (!name) continue;
      paramDefaults[name] = node.content ?? '';
    }
  }

  return {
    skill: {
      name: meta.name,
      description: meta.description,
      category: meta.category ?? 'workflow',
      skill_type: 'workflow',
      workflow_steps: steps,
      param_defaults: Object.keys(paramDefaults).length > 0 ? paramDefaults : {},
      is_published: false,
    },
    warnings,
  };
}

// ──────────────────────────────────────────────────────────────────────
// 解码：Skill JSON → canvas nodes + connections
// ──────────────────────────────────────────────────────────────────────

const PLACEHOLDER_RE = /\{\{\s*(step|input)\.([a-zA-Z0-9_]+)(?:\.([a-zA-Z0-9_]+))?\s*\}\}/g;

export function decodeFromSkill(skill: SkillExport): {
  nodes: CanvasNode[];
  connections: Connection[];
  warnings: string[];
} {
  const warnings: string[] = [];
  const nodes: CanvasNode[] = [];
  const connections: Connection[] = [];
  const nodeIdByName = new Map<string, string>();

  // 1) 预先创建被任意 step 的 inputs 引用到的输入节点。布局放在左侧列，x = 80。
  const inputNames = new Set<string>();
  for (const step of skill.workflow_steps ?? []) {
    walkPlaceholders(step.inputs, (kind, name) => {
      if (kind === 'input') inputNames.add(name);
    });
  }
  let inputY = 80;
  for (const name of inputNames) {
    const id = freshId();
    nodeIdByName.set(name, id);
    nodes.push({
      id,
      type: 'text-node',
      x: 80,
      y: inputY,
      width: 240,
      height: 140,
      content: '',
      settings: { initialVarName: name },
    });
    inputY += 200;
  }

  // 2) step 按拓扑序号从左到右排布。
  let column = 1;
  let stepY = 80;
  for (const step of skill.workflow_steps ?? []) {
    const nodeType = TOOL_TO_NODE[step.tool_name];
    if (!nodeType) {
      warnings.push(`未知 tool_name: ${step.tool_name}（step ${step.name}），已跳过`);
      continue;
    }
    const id = freshId();
    nodeIdByName.set(step.name, id);
    const inputs = step.inputs ?? {};
    const settings: Record<string, unknown> = {};
    if (typeof inputs.prompt === 'string' && !isPlaceholder(inputs.prompt)) {
      settings.prompt = inputs.prompt;
    }
    if (typeof inputs.ratio === 'string') settings.ratio = inputs.ratio;
    if (typeof inputs.resolution === 'string') settings.resolution = inputs.resolution;
    if (typeof inputs.duration === 'number') settings.duration = inputs.duration;

    nodes.push({
      id,
      type: nodeType,
      x: 80 + column * 320,
      y: stepY,
      width: 280,
      height: 220,
      settings,
    });
    column++;
    stepY += 240;
  }

  // 3) 第二遍——把 step inputs 里的占位符解析成 connection。
  for (const step of skill.workflow_steps ?? []) {
    const toId = nodeIdByName.get(step.name);
    if (!toId) continue;
    const inputs = step.inputs ?? {};
    const addRef = (placeholder: string, inputType: string) => {
      const ref = parsePlaceholder(placeholder);
      if (!ref) return;
      const fromId = nodeIdByName.get(ref.name);
      if (!fromId) {
        warnings.push(`step ${step.name} 引用了未知节点 ${ref.name}`);
        return;
      }
      connections.push({ id: freshId(), from: fromId, to: toId, inputType });
    };
    for (const u of asArray(inputs.images)) if (typeof u === 'string') addRef(u, 'image');
    for (const u of asArray(inputs.videos)) if (typeof u === 'string') addRef(u, 'video');
    for (const u of asArray(inputs.audios)) if (typeof u === 'string') addRef(u, 'audio');
    if (typeof inputs.prompt === 'string' && isPlaceholder(inputs.prompt)) {
      addRef(inputs.prompt, 'text');
    }
  }

  return { nodes, connections, warnings };
}

// ──────────────────────────────────────────────────────────────────────
// 辅助函数
// ──────────────────────────────────────────────────────────────────────

function topologicalSort(
  nodes: CanvasNode[],
  connections: Connection[],
  warnings: string[],
): CanvasNode[] {
  const inDegree = new Map<string, number>();
  for (const n of nodes) inDegree.set(n.id, 0);
  for (const c of connections) {
    inDegree.set(c.to, (inDegree.get(c.to) ?? 0) + 1);
  }
  const queue: string[] = [];
  for (const [id, deg] of inDegree) if (deg === 0) queue.push(id);
  const order: CanvasNode[] = [];
  while (queue.length > 0) {
    const id = queue.shift()!;
    const node = nodes.find((n) => n.id === id);
    if (node) order.push(node);
    for (const c of connections.filter((c) => c.from === id)) {
      const next = (inDegree.get(c.to) ?? 0) - 1;
      inDegree.set(c.to, next);
      if (next === 0) queue.push(c.to);
    }
  }
  if (order.length < nodes.length) {
    warnings.push('画布存在环依赖，部分节点未参与编译');
  }
  return order;
}

// 只对「能编译成 step 或 input 的节点类型」有意义：调用点在上游未注册时已经
// continue 掉了，所以后处理节点（subtitle-erase / video-upscale / video-whitemodel）不需要分支——
// 它们不在 NODE_TO_TOOL/INPUT_NODE_TYPES/SINK_NODE_TYPES 里，编译时整个被跳过并
// 记一条 warning。「发布 workflow 不支持后处理节点」是既存功能缺口，不在此处兜。
function guessOutputKind(t: NodeType): 'image' | 'video' | 'audio' | 'text' {
  if (t === 'gen-image' || t === 'input-image') return 'image';
  if (t === 'gen-video' || t === 'video-input') return 'video';
  if (t === 'input-audio' || t === 'gen-audio') return 'audio';
  return 'text';
}

function slug(t: string): string {
  return t.replace(/[^a-zA-Z0-9]+/g, '_');
}

function freshId(): string {
  return `n_${Math.random().toString(36).slice(2, 10)}`;
}

function isPlaceholder(s: string): boolean {
  PLACEHOLDER_RE.lastIndex = 0;
  return PLACEHOLDER_RE.test(s);
}

function parsePlaceholder(s: string): { kind: 'step' | 'input'; name: string } | null {
  PLACEHOLDER_RE.lastIndex = 0;
  const m = PLACEHOLDER_RE.exec(s);
  if (!m) return null;
  return { kind: m[1] as 'step' | 'input', name: m[2] };
}

function walkPlaceholders(
  value: unknown,
  visit: (kind: 'step' | 'input', name: string) => void,
): void {
  if (typeof value === 'string') {
    PLACEHOLDER_RE.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = PLACEHOLDER_RE.exec(value)) != null) {
      visit(m[1] as 'step' | 'input', m[2]);
    }
    return;
  }
  if (Array.isArray(value)) {
    for (const v of value) walkPlaceholders(v, visit);
    return;
  }
  if (value && typeof value === 'object') {
    for (const v of Object.values(value)) walkPlaceholders(v, visit);
  }
}

function asArray(v: unknown): unknown[] {
  return Array.isArray(v) ? v : [];
}
