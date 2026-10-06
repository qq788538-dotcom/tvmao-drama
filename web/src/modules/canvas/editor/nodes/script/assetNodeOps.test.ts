import { beforeEach, describe, expect, it } from 'vitest';
import { useCanvasStore } from '../../store';
import { createGenAssetNode, createUploadAssetNode, assetLinkedNodeAlive } from './assetNodeOps';

describe('assetNodeOps', () => {
  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [{ id: 's1', type: 'script', x: 1000, y: 500, width: 300, height: 220, settings: {} }],
      connections: [], readOnly: false,
    });
  });

  it('createUploadAssetNode 建 input-image 于脚本节点左侧并连线', () => {
    const id = createUploadAssetNode('s1', 0);
    const st = useCanvasStore.getState();
    const node = st.nodes.find((n) => n.id === id)!;
    expect(node.type).toBe('input-image');
    expect(node.x).toBeLessThan(1000);
    expect(st.connections.some((c) => c.from === id && c.to === 's1')).toBe(true);
  });

  it('落位是每列 4 个的网格：第 5 个资产换列回首行,不再单列排到几千像素外', () => {
    const first = createUploadAssetNode('s1', 0);
    const fifth = createUploadAssetNode('s1', 4);
    const st = useCanvasStore.getState();
    const n0 = st.nodes.find((n) => n.id === first)!;
    const n4 = st.nodes.find((n) => n.id === fifth)!;
    expect(n4.x).toBeLessThan(n0.x); // 第二列更靠左
    expect(n4.y).toBe(n0.y); // 回到首行
  });

  it('createGenAssetNode 建 gen-image 并预填 prompt', () => {
    const id = createGenAssetNode('s1', 1, '角色设定图提示词');
    const node = useCanvasStore.getState().nodes.find((n) => n.id === id)!;
    expect(node.type).toBe('gen-image');
    expect(node.settings?.prompt).toBe('角色设定图提示词');
  });

  it('assetLinkedNodeAlive 判断节点存活', () => {
    const id = createUploadAssetNode('s1', 0);
    expect(assetLinkedNodeAlive(id)).toBe(true);
    useCanvasStore.getState().deleteNodes([id]);
    expect(assetLinkedNodeAlive(id)).toBe(false);
  });
});
