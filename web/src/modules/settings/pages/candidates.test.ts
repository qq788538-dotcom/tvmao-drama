import { describe, expect, it } from 'vitest';
import { DEFAULT_MAX_RETRIES, moveItem, postprocessErrors, scriptErrors, upscaleMode } from './candidates';

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

describe('scriptErrors：空候选与数值范围（与 sidecar 一致）', () => {
  const ok = [{ modelId: 'a', displayName: 'A' }];
  it('候选列表不能为空', () => {
    expect(scriptErrors({ modelCandidates: [], styles: [] })).toEqual(['至少保留一个候选模型']);
  });
  it('重跑次数只接受 1–10 的整数，缺省按 1 处理', () => {
    expect(DEFAULT_MAX_RETRIES).toBe(1);
    expect(scriptErrors({ modelCandidates: ok, styles: [] })).toEqual([]);
    for (const n of [1, 10]) expect(scriptErrors({ modelCandidates: ok, styles: [], maxRetries: n })).toEqual([]);
    for (const n of [0, 11, -1, 1.5]) {
      expect(scriptErrors({ modelCandidates: ok, styles: [], maxRetries: n })).toEqual(['失败自动重跑次数必须是 1 到 10 的整数']);
    }
  });
  it('并发数只接受 1–20 的整数', () => {
    expect(scriptErrors({ modelCandidates: ok, styles: [], concurrency: 0 })).toEqual(['同时执行的任务数必须是 1 到 20 的整数']);
    expect(scriptErrors({ modelCandidates: ok, styles: [], concurrency: 20 })).toEqual([]);
  });
});

describe('postprocessErrors', () => {
  it('去字幕选项必须选择模型', () => {
    expect(postprocessErrors({ subtitleRemovalModels: [{ modelId: 'a', label: 'A' }, { modelId: '', label: 'B' }] })).toEqual(['第 2 个去字幕选项没有选择模型']);
    expect(postprocessErrors({ subtitleRemovalModels: [{ modelId: '  ', label: 'A' }] })).toEqual(['第 1 个去字幕选项没有选择模型']);
    expect(postprocessErrors({ subtitleRemovalModels: [{ modelId: 'a', label: 'A' }] })).toEqual([]);
    expect(postprocessErrors({})).toEqual([]);
  });
});
