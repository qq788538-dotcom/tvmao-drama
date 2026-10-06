import { beforeEach, describe, expect, it } from 'vitest';
import { useCanvasStore } from '../../store';
import { pickClosestDurationOption, spawnVideoNodes, spawnedCountForRow, translateRow } from './batchVideo';
import type { ScriptAssets } from './scriptData';

const assets: ScriptAssets = {
  characters: [{ id: 'c1', name: '张三', description: '', linkedNodeId: 'img1' }],
  scenes: [{ id: 's1', name: '修理铺', description: '', linkedNodeId: 'img2' }],
  props: [],
};

function seed(rows: unknown[]) {
  useCanvasStore.setState({
    nodes: [
      { id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220, settings: { rows, assets } },
      { id: 'img1', type: 'input-image', x: -400, y: 0, width: 200, height: 200, content: 'https://x/1.png' },
      { id: 'img2', type: 'input-image', x: -400, y: 300, width: 200, height: 200, content: 'https://x/2.png' },
    ],
    connections: [], readOnly: false,
  });
}

describe('batchVideo（skill 对齐版）', () => {
  beforeEach(() => seed([]));

  it('translateRow：token→@[图片:nodeId]，缺图退化纯名字并进 missing', () => {
    seed([]);
    const row = { id: 'r1', shotNumber: 1, name: '', duration: 5, colorMark: '' as const,
      prompt: '镜头1：@[角色-张三]在@[场景-修理铺]，@[物品-信封]在桌上。' };
    const t = translateRow(row, assets);
    expect(t.text).toBe('镜头1：@[图片:img1]在@[图片:img2]，信封在桌上。');
    expect(t.refNodeIds).toEqual(['img1', 'img2']);
    expect(t.missing).toEqual([{ kind: 'prop', name: '信封' }]);
  });

  it('spawnVideoNodes：铺 gen-video、写翻译后的 settings+uiSchema 参数、只连参考图不连脚本节点、不提交生成', () => {
    seed([{ id: 'r1', shotNumber: 1, name: '开场', duration: 7, colorMark: '',
      prompt: '镜头1：@[角色-张三]走进@[场景-修理铺]。' }]);
    const ids = spawnVideoNodes('s1', [{ rowId: 'r1', duration: '10' }], 'seedance-1', { ratio: '16:9', resolution: '720P' });
    expect(ids).toHaveLength(1);
    const st = useCanvasStore.getState();
    const vn = st.nodes.find((n) => n.id === ids[0])!;
    expect(vn.type).toBe('gen-video');
    expect(vn.settings).toMatchObject({
      videoPrompt: '镜头1：@[图片:img1]走进@[图片:img2]。',
      modelId: 'seedance-1',
      scriptRowId: 'r1',
      name: '开场', // 节点标题=分镜简述(NodeShell settings.name)
      duration: '10',
      ratio: '16:9',
      resolution: '720P',
    });
    expect(vn.status).toBe('idle');
    expect(vn.x).toBeGreaterThan(300);
    for (const from of ['img1', 'img2']) {
      expect(st.connections.some((c) => c.from === from && c.to === ids[0])).toBe(true);
    }
    // 用户拍板：不连脚本节点（scriptRowId 反向锚已表达归属，连线只是噪音）。
    expect(st.connections.some((c) => c.from === 's1' && c.to === ids[0])).toBe(false);
    expect(spawnedCountForRow('r1')).toBe(1);
  });

  it('pickClosestDurationOption 就近取档位', () => {
    const field = { label: '时长', default: '5', options: [
      { label: '5s', value: '5' }, { label: '10s', value: '10' },
    ] };
    expect(pickClosestDurationOption(field as never, 7)).toBe('5');
    expect(pickClosestDurationOption(field as never, 9)).toBe('10');
  });
});
