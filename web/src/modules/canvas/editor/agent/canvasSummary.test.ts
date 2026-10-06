import { describe, it, expect } from 'vitest';

import type { CanvasNode } from '../types';
import { buildCanvasSummary } from './canvasSummary';

const baseNode = (over: Partial<CanvasNode>): CanvasNode => ({
  id: 'n0',
  type: 'preview',
  x: 0,
  y: 0,
  width: 100,
  height: 100,
  content: 'http://x/0.png',
  ...over,
});

describe('buildCanvasSummary', () => {
  it('returns empty for empty input', () => {
    expect(buildCanvasSummary([])).toEqual([]);
  });

  it('keeps media nodes (preview / gen / input) and drops text', () => {
    const nodes: CanvasNode[] = [
      baseNode({ id: 'p1', type: 'preview', content: 'http://x/1.png' }),
      baseNode({ id: 'g1', type: 'gen-image', content: 'http://x/2.png' }),
      baseNode({ id: 't1', type: 'text-node', content: 'hello' }),
      baseNode({ id: 'i1', type: 'input-image', content: 'http://x/3.png' }),
    ];
    const out = buildCanvasSummary(nodes);
    // 倒序返回：画布数组按创建顺序 append，@ 候选要最新的在前。
    expect(out.map((n) => n.id)).toEqual(['i1', 'g1', 'p1']);
  });

  it('returns all nodes without cap, newest first', () => {
    const nodes: CanvasNode[] = Array.from({ length: 50 }, (_, i) =>
      baseNode({ id: `n${i}`, type: 'preview', content: `http://x/${i}.png` }),
    );
    const out = buildCanvasSummary(nodes);
    // 不截断：候选列表只服务 @ 引用（不进 prompt），截断会让老节点永远 @ 不到。
    expect(out).toHaveLength(50);
    // 最新的节点（n49）排在最前面。
    expect(out[0].id).toBe('n49');
    expect(out[out.length - 1].id).toBe('n0');
  });

  it('uses settings.name when set', () => {
    const nodes: CanvasNode[] = [
      baseNode({ id: 'p1', type: 'preview', content: 'http://x/1.png', settings: { name: '柴犬' } }),
    ];
    expect(buildCanvasSummary(nodes)[0].name).toBe('柴犬');
  });

  it('text-node with settings.text is included as kind=text carrying full text', () => {
    const longText = '这是一段很长的剧本正文，用来验证名字截断与正文透传。';
    const nodes: CanvasNode[] = [
      baseNode({
        id: 't1',
        type: 'text-node',
        content: undefined,
        settings: { text: longText },
      }),
      baseNode({
        id: 't2',
        type: 'text-node',
        content: undefined,
        settings: { text: '正文', name: '短片剧本《暴雨单》' },
      }),
    ];
    const out = buildCanvasSummary(nodes);
    expect(out).toHaveLength(2);
    // 倒序：t2 在前。
    const t2 = out[0];
    const t1 = out[1];
    // 有 settings.name 用 name；没有则取正文前 20 字。
    expect(t2).toMatchObject({ id: 't2', kind: 'text', url: '', name: '短片剧本《暴雨单》', text: '正文' });
    expect(t1.kind).toBe('text');
    expect(t1.name).toBe(longText.slice(0, 20));
    expect(t1.text).toBe(longText);
  });

  it('text-node without text (or empty) is excluded', () => {
    const nodes: CanvasNode[] = [
      baseNode({ id: 't1', type: 'text-node', content: undefined, settings: {} }),
      baseNode({ id: 't2', type: 'text-node', content: undefined, settings: { text: '' } }),
      // content 有值但没有 settings.text——文字节点不看 content。
      baseNode({ id: 't3', type: 'text-node', content: 'hello' }),
    ];
    expect(buildCanvasSummary(nodes)).toEqual([]);
  });

  it('skips media nodes with empty content', () => {
    const nodes: CanvasNode[] = [
      baseNode({ id: 'p1', type: 'preview', content: '' }),
      baseNode({ id: 'p2', type: 'gen-image' }),
      baseNode({ id: 'p3', type: 'preview', content: 'http://x/3.png' }),
    ];
    // p2 用的是 baseNode 的默认 content（truthy），但这里明确验证
    // 空字符串会被丢掉。
    const out = buildCanvasSummary(nodes);
    expect(out.map((n) => n.id).sort()).toEqual(['p2', 'p3']);
  });
});
