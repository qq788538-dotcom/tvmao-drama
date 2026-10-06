import type { ModelPatch, ModelView } from '../api';

export interface ModelFormValues {
  name: string;
  sortOrder: number;
  enabled: boolean;
  hidden: boolean;
  uiDefaults: Record<string, string | number>;
  concurrency: number;
  canvasMaxRetries: number;
  canvasRetryIntervalSec: number;
}

export const MODEL_FIELD_LABELS: Record<string, string> = {
  enabled: '启用',
  hidden: '隐藏',
  name: '显示名',
  sortOrder: '排序',
  uiSchemaDefaults: '参数默认值',
  concurrency: '并发上限',
  canvasMaxRetries: '最大重试次数',
  canvasRetryIntervalSec: '重试间隔',
};

export const TYPE_OPTIONS = [
  { value: 'all', label: '全部' },
  { value: 'image', label: '图片' },
  { value: 'video', label: '视频' },
  { value: 'audio', label: '音频' },
  { value: 'text', label: '文本' },
];

export function modelToForm(m: ModelView): ModelFormValues {
  const uiDefaults: Record<string, string | number> = {};
  for (const [k, f] of Object.entries(m.uiSchema ?? {})) if (f.default !== undefined) uiDefaults[k] = f.default;
  const canvas = m.genControl?.modules?.canvas;
  return {
    name: m.name,
    sortOrder: m.sortOrder,
    enabled: m.enabled,
    hidden: m.hidden,
    uiDefaults,
    concurrency: m.genControl?.concurrency ?? 0,
    canvasMaxRetries: canvas?.maxRetries ?? 0,
    canvasRetryIntervalSec: canvas?.retryIntervalSec ?? 0,
  };
}

/** 只放进与原值不同的字段；服务端严格拒绝补丁之外的任何字段（spec M10）。清空的数字框（null）视为未改动。 */
export function formToPatch(m: ModelView, v: ModelFormValues): ModelPatch {
  const before = modelToForm(m);
  const p: ModelPatch = {};
  const name = v.name.trim();
  if (name !== before.name) p.name = name;
  if (v.sortOrder != null && v.sortOrder !== before.sortOrder) p.sortOrder = v.sortOrder;
  if (v.enabled !== before.enabled) p.enabled = v.enabled;
  if (v.hidden !== before.hidden) p.hidden = v.hidden;
  if (v.concurrency != null && v.concurrency !== before.concurrency) p.concurrency = v.concurrency;
  if (v.canvasMaxRetries != null && v.canvasMaxRetries !== before.canvasMaxRetries) p.canvasMaxRetries = v.canvasMaxRetries;
  if (v.canvasRetryIntervalSec != null && v.canvasRetryIntervalSec !== before.canvasRetryIntervalSec) p.canvasRetryIntervalSec = v.canvasRetryIntervalSec;
  const defaults: Record<string, string | number> = {};
  for (const [k, val] of Object.entries(v.uiDefaults ?? {})) if (val !== before.uiDefaults[k]) defaults[k] = val;
  if (Object.keys(defaults).length) p.uiSchemaDefaults = defaults;
  return p;
}

export function filterModels(list: ModelView[], type: string, keyword: string): ModelView[] {
  const kw = keyword.trim().toLowerCase();
  return list.filter(
    (m) => (type === 'all' || m.type === type) && (!kw || m.name.toLowerCase().includes(kw) || m.modelId.toLowerCase().includes(kw)),
  );
}
