import { currentProjectId } from '@/shared/api';
import { useCanvasStore } from '../store';
import type { CanvasNode, Connection } from '../types';

/**
 * 「复制节点」的剪贴板层（对标 libtv）。
 *
 * 机制：节点 JSON 以**自定义 MIME** 写进系统剪贴板——外部应用（微信/备忘录）
 * 只认 text/plain、图片等标准格式，看不见这个格式，所以外面什么都粘不出；
 * 画布的 paste 事件里 getData 同一 MIME 就能拿回来。因为用的就是系统剪贴板
 * 本体，覆盖/排序天然遵循系统语义：复制节点后又复制了别的，节点数据就被
 * 覆盖，粘贴行为与用户直觉一致。
 *
 * localStorage 镜像只服务一条窄路：右键菜单「粘贴」是鼠标点击，没有 paste
 * 事件，异步 clipboard API 又读不到自定义 MIME（Chromium 的 web custom
 * format 是专属 API，不为它上），所以菜单粘贴读镜像。代价是镜像感知不到
 * 系统剪贴板之后的覆盖——歧义只残留在「复制节点→又复制了别的→点菜单粘贴」
 * 这一条路径上，⌘V 路径永远精确。
 */

export const NODE_CLIPBOARD_MIME = 'application/x-tvmao-canvas-nodes';
const MIRROR_KEY = 'tvmao:canvas-node-clipboard';

export interface NodeClipboardPayload {
  format: 'tvmao-canvas-nodes';
  version: 1;
  /** 复制发生时所在的项目。粘贴侧对比它决定要不要做跨项目资产登记。 */
  sourceProjectId: number;
  nodes: CanvasNode[];
  connections: Connection[];
}

export function buildPayload(ids: string[]): NodeClipboardPayload | null {
  const data = useCanvasStore.getState().buildCopyPayload(ids);
  if (!data) return null;
  return {
    format: 'tvmao-canvas-nodes',
    version: 1,
    sourceProjectId: currentProjectId() ?? 0,
    ...data,
  };
}

/** copy 事件进行中的「指定复制这些节点」覆盖。菜单入口用：菜单点的节点
 *  不一定在 selectedIds 里，靠它把目标传给全局 copy 监听。 */
let pendingCopyIds: string[] | null = null;

export function pendingCopyOverride(): string[] | null {
  return pendingCopyIds;
}

/**
 * 菜单入口的复制：execCommand('copy') 在点击手势里合法，会触发一次真实的
 * copy 事件，由 CanvasEditor 的全局 copy 监听完成写入（走 pendingCopyOverride
 * 拿到 ids）。不直接在这里写 navigator.clipboard——异步 API 写不了自定义 MIME。
 *
 * execCommand 失败（极端环境）时降级为只写镜像：菜单粘贴仍可用，只丢
 * ⌘V 跨路径与系统覆盖语义。
 */
export function copyNodesNow(ids: string[]): boolean {
  pendingCopyIds = ids;
  let ok = false;
  try {
    ok = document.execCommand('copy');
  } catch {
    ok = false;
  } finally {
    pendingCopyIds = null;
  }
  if (!ok) {
    const payload = buildPayload(ids);
    if (!payload) return false;
    writeMirror(payload);
  }
  return true;
}

/** 节点右键菜单的「复制节点」：点的节点在多选里就复制整个选中集，
 *  否则只复制这一个——与主流画布工具(Figma 等)的选中语义一致。 */
export function copyNodeOrSelection(nodeId: string): boolean {
  const sel = useCanvasStore.getState().selectedIds;
  const ids = sel.has(nodeId) ? [...sel] : [nodeId];
  return copyNodesNow(ids);
}

export function writeMirror(payload: NodeClipboardPayload): void {
  try {
    localStorage.setItem(MIRROR_KEY, JSON.stringify(payload));
  } catch {
    // localStorage 满/被禁：镜像丢失只影响菜单粘贴，⌘V 路径不受影响，不打断复制。
  }
}

export function readMirror(): NodeClipboardPayload | null {
  try {
    const raw = localStorage.getItem(MIRROR_KEY);
    return raw ? parsePayload(raw) : null;
  } catch {
    return null;
  }
}

/** 从 paste 事件拿节点 payload（⌘V 路径）。 */
export function payloadFromClipboardEvent(e: ClipboardEvent): NodeClipboardPayload | null {
  const raw = e.clipboardData?.getData(NODE_CLIPBOARD_MIME);
  return raw ? parsePayload(raw) : null;
}

/** 解析 + 形状校验。剪贴板/localStorage 都是外部可写的，不能盲信。 */
export function parsePayload(raw: string): NodeClipboardPayload | null {
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return null;
  }
  const p = data as Partial<NodeClipboardPayload> | null;
  if (!p || p.format !== 'tvmao-canvas-nodes' || p.version !== 1) return null;
  if (!Array.isArray(p.nodes) || !Array.isArray(p.connections)) return null;
  for (const n of p.nodes) {
    if (typeof n?.id !== 'string' || typeof n?.type !== 'string') return null;
    if (typeof n?.x !== 'number' || typeof n?.y !== 'number') return null;
    if (typeof n?.width !== 'number' || typeof n?.height !== 'number') return null;
  }
  for (const c of p.connections) {
    if (typeof c?.from !== 'string' || typeof c?.to !== 'string') return null;
  }
  return p as NodeClipboardPayload;
}

/** 收集 payload 里所有 media:// 引用（content + settings 深层，含 history）。
 *  跨项目粘贴时要拿这批 object_key 去目标项目登记引用，防源项目删除后被 GC。 */
export function collectMediaKeys(payload: NodeClipboardPayload): string[] {
  const keys = new Set<string>();
  const visit = (v: unknown): void => {
    if (typeof v === 'string') {
      if (v.startsWith('media://')) {
        const key = v.slice('media://'.length);
        if (key) keys.add(key);
      }
      return;
    }
    if (Array.isArray(v)) {
      v.forEach(visit);
      return;
    }
    if (v && typeof v === 'object') {
      Object.values(v).forEach(visit);
    }
  };
  for (const n of payload.nodes) {
    visit(n.content);
    visit(n.settings);
  }
  return [...keys];
}
