import { describe, expect, it } from 'vitest';
import { groupPresets, splitPresetColumns, PRESET_COL_MAX_H } from './presetMenu';
import type { GenImagePresetItem } from '@/shared/api';

function preset(key: string, category: string, extra: Record<string, unknown> = {}): GenImagePresetItem {
  return { key, value: { name: key, category, ...extra } } as GenImagePresetItem;
}

function group(category: string, count: number, extra: Record<string, unknown> = {}) {
  return {
    category,
    items: Array.from({ length: count }, (_, i) => preset(`${category}-${i}`, category, extra)),
  };
}

describe('groupPresets', () => {
  it('按首次出现排组、空 category 归「其他」放最后', () => {
    const groups = groupPresets([
      preset('a', '分镜叙事'),
      preset('b', ''),
      preset('c', '设定图'),
      preset('d', '分镜叙事'),
    ]);
    expect(groups.map((g) => g.category)).toEqual(['分镜叙事', '设定图', '其他']);
    expect(groups[0].items.map((p) => p.key)).toEqual(['a', 'd']);
  });

  it('组内按 sort 升序', () => {
    const groups = groupPresets([
      preset('late', 'x', { sort: 20 }),
      preset('early', 'x', { sort: 1 }),
    ]);
    expect(groups[0].items.map((p) => p.key)).toEqual(['early', 'late']);
  });
});

describe('splitPresetColumns', () => {
  it('放得下就单列', () => {
    const cols = splitPresetColumns([group('a', 3), group('b', 3)]);
    expect(cols.length).toBe(1);
  });

  it('放不下从组边界换列，组不从中间劈开', () => {
    // 每组 28 + 5×48 = 268：两组 536 > 388 → 第二组整组去第二列。
    const cols = splitPresetColumns([group('a', 5), group('b', 5)]);
    expect(cols.length).toBe(2);
    expect(cols[0].map((g) => g.category)).toEqual(['a']);
    expect(cols[1].map((g) => g.category)).toEqual(['b']);
  });

  it('带 description 的条目按更高估算，会更早换列', () => {
    // 无描述 3+3：(28+144)×2=344 ≤388 单列；带描述 3+3：(28+168)×2=392 >388 双列。
    const noDesc = splitPresetColumns([group('a', 3), group('b', 3)]);
    expect(noDesc.length).toBe(1);
    const withDesc = splitPresetColumns([
      group('a', 3, { description: 'd' }),
      group('b', 3, { description: 'd' }),
    ]);
    expect(withDesc.length).toBe(2);
  });

  it('列数封顶后剩余组进最后一列（兜底滚动）', () => {
    const many = Array.from({ length: 8 }, (_, i) => group(`g${i}`, 5));
    const cols = splitPresetColumns(many, 3);
    expect(cols.length).toBe(3);
    expect(cols.flat().length).toBe(8);
    // 前两列各一组（268+268>388 换列），其余全部堆在最后一列。
    expect(cols[2].length).toBeGreaterThan(1);
  });

  it('单组超高独占一列，不劈开', () => {
    const huge = group('a', 20); // 28+960 远超 388
    const cols = splitPresetColumns([huge, group('b', 2)]);
    expect(cols[0].map((g) => g.category)).toEqual(['a']);
    expect(cols[1].map((g) => g.category)).toEqual(['b']);
  });

  it('空输入返回空数组', () => {
    expect(splitPresetColumns([])).toEqual([]);
    expect(PRESET_COL_MAX_H).toBeGreaterThan(0);
  });
});
