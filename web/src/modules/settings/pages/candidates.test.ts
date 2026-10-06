import { describe, expect, it } from 'vitest';
import { moveItem, scriptErrors, upscaleMode } from './candidates';

describe('upscaleMode', () => {
  it('有列表用列表，否则有旧单字段用单字段，都没有则 none', () => {
    expect(upscaleMode({ upscaleModels: [{ modelId: 'a', label: '' }] })).toBe('list');
    expect(upscaleMode({ upscaleModels: [], upscaleModel: 'a' })).toBe('legacy');
    expect(upscaleMode({ upscaleModel: 'a' })).toBe('legacy');
    expect(upscaleMode({})).toBe('none');
  });
});

describe('scriptErrors', () => {
  it('候选模型不能为空、不能重复，展示名不能为空', () => {
    expect(scriptErrors({ modelCandidates: [{ modelId: '', displayName: 'x' }], styles: [] })).toContain('第 1 个候选模型未选择');
    expect(scriptErrors({ modelCandidates: [{ modelId: 'a', displayName: '' }, { modelId: 'a', displayName: '' }], styles: [] })).toContain('候选模型重复：a');
    expect(scriptErrors({ modelCandidates: [{ modelId: 'a', displayName: ' ' }], styles: [] })).toEqual(['第 1 个候选模型的展示名不能为空']);
    expect(scriptErrors({ modelCandidates: [{ modelId: 'a', displayName: 'A' }], styles: [] })).toEqual([]);
  });
});

describe('moveItem', () => {
  it('返回新数组，原数组不变', () => {
    const a = [1, 2, 3];
    expect(moveItem(a, 2, 0)).toEqual([3, 1, 2]);
    expect(a).toEqual([1, 2, 3]);
    expect(moveItem(a, 0, -1)).toEqual([1, 2, 3]);
  });
});
