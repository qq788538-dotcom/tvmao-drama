import { describe, expect, it } from 'vitest';
import { DEFAULT_MAX_RETRIES, postprocessErrors, postprocessPayload, scriptErrors, scriptPayload, upscaleMode } from './candidates';

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

describe('scriptPayload / postprocessPayload', () => {
  it('脚本：只采纳 displayName、concurrency、maxRetries，条目增删、顺序、modelId 取原值', () => {
    const orig = { modelCandidates: [{ modelId: 'a', displayName: 'A' }, { modelId: 'b', displayName: 'B' }], styles: [], concurrency: 1 };
    const draft = { ...orig, modelCandidates: [{ modelId: 'x', displayName: 'A2' }], concurrency: 3, maxRetries: 2 };
    expect(scriptPayload(orig, draft)).toEqual({
      modelCandidates: [{ modelId: 'a', displayName: 'A2' }, { modelId: 'b', displayName: 'B' }],
      styles: [],
      concurrency: 3,
      maxRetries: 2,
    });
  });
  it('后处理：去字幕条目只采纳 label、subtitle、isTimeLimited；whiteModelModel 采纳草稿值', () => {
    const orig = { subtitleRemovalModels: [{ modelId: 'v1', label: 'L', subtitle: 's' }], whiteModelModel: 'w' };
    const draft = { subtitleRemovalModels: [{ modelId: 'zzz', label: 'L2', subtitle: 's2', isTimeLimited: true }, { modelId: 'n', label: 'n' }], whiteModelModel: 'w2' };
    expect(postprocessPayload(orig, draft)).toEqual({
      subtitleRemovalModels: [{ modelId: 'v1', label: 'L2', subtitle: 's2', isTimeLimited: true }],
      whiteModelModel: 'w2',
    });
  });
});
