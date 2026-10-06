import { describe, expect, it } from 'vitest';
import { updateAt } from '../draft';
import { groupByCategory, presetErrors, type GenImagePresetsConfig } from './presets';

const cfg: GenImagePresetsConfig = {
  models: ['gpt-image-2'],
  presets: [
    { key: 'a', value: { name: '人像', category: '人物', prompt: 'p1', futureField: { x: 1 } } },
    { key: 'b', value: { name: '风景', category: '场景', prompt: 'p2' } },
    { key: 'c', value: { name: '证件照', category: '人物', prompt: 'p3', params: [{ key: 'skin', name: '皮肤', options: [{ key: 'n', name: '自然', prompt: '' }] }] } },
  ],
};

describe('presets', () => {
  it('按分类首次出现的顺序分组，保留原始下标', () => {
    const g = groupByCategory(cfg);
    expect(g.map((x) => x.category)).toEqual(['人物', '场景']);
    expect(g[0].items.map((i) => i.index)).toEqual([0, 2]);
  });
  it('keeps unknown preset fields：改提示词不丢未知字段', () => {
    const next = updateAt(cfg, ['presets', 0, 'value', 'prompt'], '新');
    expect(next.presets[0].value.futureField).toEqual({ x: 1 });
    expect(next.presets[2].value.params).toBe(cfg.presets[2].value.params);
  });
  it('提示词为空、模型为空时报错；档位提示词允许为空', () => {
    expect(presetErrors(cfg)).toEqual([]);
    const bad = updateAt(updateAt(cfg, ['presets', 1, 'value', 'prompt'], '  '), ['models'], []);
    expect(presetErrors(bad)).toEqual(['至少选择一个绘图模型', '「风景」的提示词不能为空']);
  });
});
