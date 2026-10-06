import { describe, expect, it } from 'vitest';
import { applyMergePatch } from './mergePatch';

describe('applyMergePatch (RFC 7386)', () => {
  it('null 删除该字段，而不是写成 null', () => {
    const out = applyMergePatch({ a: 1, taskId: 't1' }, { taskId: null }) as Record<string, unknown>;
    expect(out).toEqual({ a: 1 });
    expect('taskId' in out).toBe(false);
  });

  it('嵌套 null 只删嵌套字段', () => {
    expect(applyMergePatch({ settings: { error: 'x', keep: 1 } }, { settings: { error: null } })).toEqual({
      settings: { keep: 1 },
    });
  });

  it('数组整体替换，不按下标合并', () => {
    expect(applyMergePatch({ list: [1, 2, 3] }, { list: [9] })).toEqual({ list: [9] });
  });

  it('嵌套对象递归合并，未出现的字段保留', () => {
    expect(
      applyMergePatch({ a: 1, s: { x: 1, y: 2 } }, { s: { y: 3, z: 4 } }),
    ).toEqual({ a: 1, s: { x: 1, y: 3, z: 4 } });
  });

  it('不修改入参', () => {
    const target = { s: { x: 1 } };
    applyMergePatch(target, { s: { x: null } });
    expect(target).toEqual({ s: { x: 1 } });
  });
});
