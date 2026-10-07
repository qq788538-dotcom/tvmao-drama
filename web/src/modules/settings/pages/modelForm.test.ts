import { describe, expect, it } from 'vitest';
import type { ModelView } from '../api';
import { filterModels, formToPatch, modelToForm, validateUpstream } from './modelForm';

const base: ModelView = {
  id: 1, type: 'image', name: 'GPT Image 2', modelId: 'gpt-image-2', enabled: true, hidden: false, sortOrder: 1,
  basePath: '/v1/images', imageInputFormat: 'url', imageRequestMode: 'json', requestTemplate: {}, pollingTemplate: null,
  uiSchema: { ratio: { label: '比例', default: '1:1', options: [{ value: '1:1' }, { value: '16:9' }] } },
  genControl: { concurrency: 3, modules: { canvas: { maxRetries: 1, retryIntervalSec: 5 }, drama: { maxRetries: 9 } } },
  modifiedFields: [], references: [],
  upstream: { configured: false, baseUrl: '', apiKeyMasked: '' },
};

describe('modelForm', () => {
  it('modelToForm 读出可改字段，缺省值补 0', () => {
    const v = modelToForm({ ...base, genControl: null });
    expect(v).toMatchObject({ name: 'GPT Image 2', concurrency: 0, canvasMaxRetries: 0, canvasRetryIntervalSec: 0, uiDefaults: { ratio: '1:1' } });
  });
  it('未改动时补丁为空', () => {
    expect(formToPatch(base, modelToForm(base))).toEqual({});
  });
  it('只包含改动的字段，不出现 drama 模块与技术字段', () => {
    const v = { ...modelToForm(base), name: ' 新名 ', concurrency: 2, uiDefaults: { ratio: '16:9' } };
    const p = formToPatch(base, v);
    expect(p).toEqual({ name: '新名', concurrency: 2, uiSchemaDefaults: { ratio: '16:9' } });
  });
  it('清空的数字框（null）视为未改动，不进补丁', () => {
    const v = { ...modelToForm(base), sortOrder: null, concurrency: null, canvasMaxRetries: null, canvasRetryIntervalSec: null } as unknown as ReturnType<typeof modelToForm>;
    expect(formToPatch(base, v)).toEqual({});
  });
  it('filterModels 按类型和关键字（名称或 modelId）筛选', () => {
    const list = [base, { ...base, id: 2, type: 'video', name: '转高清', modelId: 'task-up' }];
    expect(filterModels(list, 'video', '').map((m) => m.id)).toEqual([2]);
    expect(filterModels(list, 'all', 'TASK').map((m) => m.id)).toEqual([2]);
  });

  describe('独立上游', () => {
    const configured: ModelView = { ...base, upstream: { configured: true, baseUrl: 'https://u.example.com', apiKeyMasked: '••••abcd' } };
    it('未改动时不带 upstream', () => {
      expect(formToPatch(configured, modelToForm(configured))).toEqual({});
      expect(formToPatch(base, modelToForm(base))).toEqual({});
    });
    it('未配置过且只填地址：校验拦下', () => {
      const v = { ...modelToForm(base), upstreamMode: 'custom' as const, upstreamBaseUrl: 'https://u.example.com' };
      expect(validateUpstream(base, v)).toContain('都必须填写');
    });
    it('已有 Key 时只改地址：通过校验，补丁不含 apiKey', () => {
      const v = { ...modelToForm(configured), upstreamBaseUrl: 'https://new.example.com' };
      expect(validateUpstream(configured, v)).toBeNull();
      const p = formToPatch(configured, v);
      expect(p.upstream).toEqual({ baseUrl: 'https://new.example.com' });
      expect(p.upstream).not.toHaveProperty('apiKey');
    });
    it('切回使用全局发 null', () => {
      expect(formToPatch(configured, { ...modelToForm(configured), upstreamMode: 'global' })).toEqual({ upstream: null });
    });
    it('新配置：地址和 Key 都带上', () => {
      const v = { ...modelToForm(base), upstreamMode: 'custom' as const, upstreamBaseUrl: ' https://u.example.com ', upstreamApiKey: ' sk-1 ' };
      expect(formToPatch(base, v).upstream).toEqual({ baseUrl: 'https://u.example.com', apiKey: 'sk-1' });
    });
  });
});
