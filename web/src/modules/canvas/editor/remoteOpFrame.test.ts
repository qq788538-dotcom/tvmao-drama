import { describe, expect, it } from 'vitest';
import { parseRemoteCanvasOp } from './remoteOpFrame';

// 帧（snake_case，后端契约）→ store 的 RemoteCanvasOp（camelCase，前端契约）。
// 这一层是唯一的翻译处，翻错一个字段名的后果是「合并进画布的是 undefined」——
// 静默、且只有多人同时编辑时才看得见，所以逐 kind 钉。
describe('parseRemoteCanvasOp', () => {
  it('upsertNode：整节点原样带过来', () => {
    const node = { id: 'n1', type: 'gen-image', x: 1, y: 2, width: 3, height: 4 };
    expect(parseRemoteCanvasOp({ kind: 'upsertNode', node })).toEqual({ kind: 'upsertNode', node });
  });

  it('patchNode：node_id → nodeId，patch 原样', () => {
    expect(parseRemoteCanvasOp({ kind: 'patchNode', node_id: 'n1', patch: { settings: { prompt: 'x' } } }))
      .toEqual({ kind: 'patchNode', nodeId: 'n1', patch: { settings: { prompt: 'x' } } });
  });

  it('deleteNodes：node_ids → nodeIds', () => {
    expect(parseRemoteCanvasOp({ kind: 'deleteNodes', node_ids: ['n1', 'n2'] }))
      .toEqual({ kind: 'deleteNodes', nodeIds: ['n1', 'n2'] });
  });

  it('patchPositions：只带服务端真的传了的几何字段', () => {
    expect(parseRemoteCanvasOp({ kind: 'patchPositions', positions: [{ id: 'n1', x: 3 }, { id: 'n2', width: 9, height: 8 }] }))
      .toEqual({ kind: 'patchPositions', positions: [{ id: 'n1', x: 3 }, { id: 'n2', width: 9, height: 8 }] });
  });

  it('deleteConnection：connection_id → connectionId', () => {
    expect(parseRemoteCanvasOp({ kind: 'deleteConnection', connection_id: 'c1' }))
      .toEqual({ kind: 'deleteConnection', connectionId: 'c1' });
  });

  it('upsertConnection：整条连线原样带过来', () => {
    const connection = { id: 'c1', from: 'n1', to: 'n2' };
    expect(parseRemoteCanvasOp({ kind: 'upsertConnection', connection }))
      .toEqual({ kind: 'upsertConnection', connection });
  });

  // 认不出来 = null，调用方据此退回整拉快照。**这是滚动部署期的必经之路**：
  // 新版后端发的新 kind 老客户端一定不认识，此时崩掉或塞进画布都是灾难。
  it('不认识的 kind → null（退回整拉，不能崩也不能瞎猜）', () => {
    expect(parseRemoteCanvasOp({ kind: 'reorderLayers', node_ids: ['n1'] })).toBeNull();
  });

  it('op 缺席 → null', () => {
    expect(parseRemoteCanvasOp(undefined)).toBeNull();
    expect(parseRemoteCanvasOp(null)).toBeNull();
  });

  it('字段缺失/类型不对 → null，绝不把 undefined 当数据合并进画布', () => {
    expect(parseRemoteCanvasOp({ kind: 'upsertNode' })).toBeNull();
    expect(parseRemoteCanvasOp({ kind: 'upsertNode', node: { type: 'gen-image' } })).toBeNull(); // 没 id
    expect(parseRemoteCanvasOp({ kind: 'patchNode', node_id: 'n1' })).toBeNull();
    expect(parseRemoteCanvasOp({ kind: 'patchNode', patch: { a: 1 } })).toBeNull();
    expect(parseRemoteCanvasOp({ kind: 'deleteNodes', node_ids: 'n1' })).toBeNull();
    expect(parseRemoteCanvasOp({ kind: 'deleteNodes', node_ids: [] })).toBeNull();
    expect(parseRemoteCanvasOp({ kind: 'patchPositions', positions: [{ x: 1 }] })).toBeNull();
    expect(parseRemoteCanvasOp({ kind: 'deleteConnection' })).toBeNull();
  });

  // 几何字段缺省 = 「不改这一维」，补成 0 会把队友的节点弹到画布左上角。
  it('几何补丁里不存在的字段不许被补成 0', () => {
    const op = parseRemoteCanvasOp({ kind: 'patchPositions', positions: [{ id: 'n1', y: 5 }] });
    expect(op).toEqual({ kind: 'patchPositions', positions: [{ id: 'n1', y: 5 }] });
    expect(Object.keys((op as unknown as { positions: Record<string, unknown>[] }).positions[0])).toEqual(['id', 'y']);
  });
});
