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
  upstreamMode: 'global' | 'custom';
  upstreamBaseUrl: string;
  upstreamApiKey: string;
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
    upstreamMode: m.upstream?.configured ? 'custom' : 'global',
    upstreamBaseUrl: m.upstream?.baseUrl ?? '',
    upstreamApiKey: '',
  };
}

const UPSTREAM_REQUIRED = '单独配置上游时，地址和 API Key 都必须填写';

/** 单独配置上游时地址和 Key 必须同时有；已存过 Key 的模型 Key 可留空（保留原 Key）。返回各字段错误文案。 */
export function upstreamFieldErrors(
  m: ModelView,
  v: Pick<ModelFormValues, 'upstreamMode' | 'upstreamBaseUrl' | 'upstreamApiKey'>,
): { baseUrl?: string; apiKey?: string } {
  if (v.upstreamMode !== 'custom') return {};
  const url = (v.upstreamBaseUrl ?? '').trim();
  const key = (v.upstreamApiKey ?? '').trim();
  const out: { baseUrl?: string; apiKey?: string } = {};
  if (!url) out.baseUrl = UPSTREAM_REQUIRED;
  else if (!/^https?:\/\/\S+$/i.test(url)) out.baseUrl = '上游地址必须是 http(s):// 开头的完整地址';
  if (!key && !m.upstream?.configured) out.apiKey = UPSTREAM_REQUIRED;
  return out;
}

export function validateUpstream(m: ModelView, v: Pick<ModelFormValues, 'upstreamMode' | 'upstreamBaseUrl' | 'upstreamApiKey'>): string | null {
  const e = upstreamFieldErrors(m, v);
  return e.baseUrl ?? e.apiKey ?? null;
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
  if (v.upstreamMode === 'global') {
    if (m.upstream?.configured) p.upstream = null;
  } else if (v.upstreamMode === 'custom') {
    const url = (v.upstreamBaseUrl ?? '').trim();
    const key = (v.upstreamApiKey ?? '').trim();
    if (!(m.upstream?.configured && url === m.upstream.baseUrl && !key)) p.upstream = key ? { baseUrl: url, apiKey: key } : { baseUrl: url };
  }
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
