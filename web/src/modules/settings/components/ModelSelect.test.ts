import { describe, expect, it } from 'vitest';
import { modelOptions } from './ModelSelect';
import type { ModelView } from '../api';

const m = (p: Partial<ModelView>): ModelView => ({
  id: 1, type: 'image', name: 'n', modelId: 'id', enabled: true, hidden: false, sortOrder: 0, basePath: '', imageInputFormat: 'url',
  imageRequestMode: 'json', requestTemplate: {}, pollingTemplate: null, uiSchema: null, genControl: null, modifiedFields: [], references: [], upstream: { configured: false, baseUrl: '', apiKeyMasked: '' }, ...p,
});

describe('modelOptions', () => {
  it('只列已启用且类型匹配的模型（隐藏的也列）', () => {
    const opts = modelOptions(
      [m({ modelId: 'a', name: 'A' }), m({ modelId: 'b', hidden: true, name: 'B' }), m({ modelId: 'c', enabled: false }), m({ modelId: 'd', type: 'text' })],
      'image',
      undefined,
    );
    expect(opts.map((o) => o.value)).toEqual(['a', 'b']);
    expect(opts[0].label).toBe('A（a）');
  });
  it('当前值不在可选范围时仍显示，标注并禁用', () => {
    const opts = modelOptions([m({ modelId: 'a' })], 'image', ['a', 'gone']);
    const gone = opts.find((o) => o.value === 'gone')!;
    expect(gone.disabled).toBe(true);
    expect(gone.label).toContain('不可用');
  });
});
