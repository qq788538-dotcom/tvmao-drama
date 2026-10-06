import { describe, expect, it } from 'vitest';
import { deepClone, updateAt } from './draft';

describe('updateAt', () => {
  it('只替换路径上的值，保留兄弟字段和未知字段，不修改原对象', () => {
    const orig = { models: ['a'], presets: [{ key: 'p', value: { prompt: 'old', futureField: { x: 1 } } }], extra: true };
    const next = updateAt(orig, ['presets', 0, 'value', 'prompt'], 'new');
    expect(next.presets[0].value.prompt).toBe('new');
    expect(next.presets[0].value.futureField).toEqual({ x: 1 });
    expect(next.extra).toBe(true);
    expect(orig.presets[0].value.prompt).toBe('old');
    expect(next.models).toBe(orig.models); // 未触及的分支复用引用
  });
  it('空路径返回新值', () => {
    expect(updateAt({ a: 1 }, [], 5)).toBe(5);
  });
  it('deepClone 断开引用', () => {
    const a = { b: [1] };
    const c = deepClone(a);
    c.b.push(2);
    expect(a.b).toEqual([1]);
  });
});
