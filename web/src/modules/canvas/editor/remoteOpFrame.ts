// canvas_changed 帧里的 op 载荷 → store 的 RemoteCanvasOp 的**唯一**翻译处。
//
// 为什么单独一层：帧是后端契约（snake_case、字段随后端演进、kind 是会增长的
// 枚举），store 的 RemoteCanvasOp 是前端稳定契约（camelCase、判别联合）。两者
// 混在一起的话，每加一种 op 就要在页面和 store 两处改，且 store 单测会被迫构造
// 线格式对象。翻译收在这一处，store 只认已经校验过的形状。
//
// **认不出来一律返回 null**，调用方据此退回「整拉快照」兜底：
//   - 新版后端发了老客户端不认识的 kind（部署滚动期必然发生）；
//   - 后端摘要序列化失败，op 字段缺席；
//   - 字段类型不对（不该发生，但收到坏数据时宁可多拉一次快照，也不能把
//     `undefined` 当节点塞进画布）。
import type { CanvasOpFramePayload } from './agent/types';
import type { RemoteCanvasOp, RemoteNodeGeometry } from './store';
import type { CanvasNode, Connection } from './types';

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** 帧里的节点/连线必须是带非空 id 的对象——没有 id 就没法定位，合并进去只会
 *  在画布上多出一个永远对不上服务端的幽灵。 */
function withId(value: unknown): (Record<string, unknown> & { id: string }) | null {
  if (!isRecord(value)) return null;
  const id = value.id;
  return typeof id === 'string' && id !== '' ? (value as Record<string, unknown> & { id: string }) : null;
}

function parseGeometry(value: unknown): RemoteNodeGeometry | null {
  const item = withId(value);
  if (!item) return null;
  const geometry: RemoteNodeGeometry = { id: item.id };
  // 只带过来「服务端真的传了」的字段：缺失字段代表「不改」，补成 0 会把
  // 队友的节点弹到左上角。
  if (typeof item.x === 'number') geometry.x = item.x;
  if (typeof item.y === 'number') geometry.y = item.y;
  if (typeof item.width === 'number') geometry.width = item.width;
  if (typeof item.height === 'number') geometry.height = item.height;
  return geometry;
}

export function parseRemoteCanvasOp(op: CanvasOpFramePayload | undefined | null): RemoteCanvasOp | null {
  if (!op || typeof op !== 'object') return null;
  switch (op.kind) {
    case 'upsertNode': {
      const node = withId(op.node);
      return node ? { kind: 'upsertNode', node: node as unknown as CanvasNode } : null;
    }
    case 'patchNode': {
      if (!op.node_id || !isRecord(op.patch)) return null;
      return { kind: 'patchNode', nodeId: op.node_id, patch: op.patch };
    }
    case 'deleteNodes': {
      if (!Array.isArray(op.node_ids)) return null;
      const ids = op.node_ids.filter((id): id is string => typeof id === 'string' && id !== '');
      // 空列表不是「删所有」，是一次无意义的 op；直接判 null 走兜底，
      // 免得调用方还要区分「翻译成功但没内容」。
      return ids.length > 0 ? { kind: 'deleteNodes', nodeIds: ids } : null;
    }
    case 'patchPositions': {
      if (!Array.isArray(op.positions)) return null;
      const positions = op.positions
        .map(parseGeometry)
        .filter((item): item is RemoteNodeGeometry => item !== null);
      return positions.length > 0 ? { kind: 'patchPositions', positions } : null;
    }
    case 'upsertConnection': {
      const connection = withId(op.connection);
      return connection
        ? { kind: 'upsertConnection', connection: connection as unknown as Connection }
        : null;
    }
    case 'deleteConnection': {
      return op.connection_id ? { kind: 'deleteConnection', connectionId: op.connection_id } : null;
    }
    default:
      return null;
  }
}
