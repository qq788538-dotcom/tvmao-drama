// 设置页与 sidecar（/api/v1/settings/*）和 Tauri 壳之间的全部调用。
// 接口契约见 docs/superpowers/plans/2026-10-06-p4-settings.md「HTTP 接口一览」。
import { invoke } from '@tauri-apps/api/core';
import { getVersion } from '@tauri-apps/api/app';
import { authHeaders, buildHttpUrl, getServerInfo } from '@/shared/server';

export class SettingsApiError extends Error {
  constructor(public status: number, message: string, public field?: string) {
    super(message);
    this.name = 'SettingsApiError';
  }
}

export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

async function request<T>(method: 'GET' | 'PUT' | 'PATCH' | 'POST', path: string, body?: unknown): Promise<T> {
  const info = await getServerInfo();
  const headers: Record<string, string> = { ...authHeaders(info) };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  let resp: Response;
  try {
    resp = await fetch(buildHttpUrl(info, `/api/v1/settings${path}`), {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    // 网络层失败时浏览器给的是英文 "Failed to fetch"，换成中文提示。
    throw new Error('连接本地服务失败，请稍后重试');
  }
  const text = await resp.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = null;
  }
  if (!resp.ok) {
    throw new SettingsApiError(resp.status, data?.error ?? `请求失败（HTTP ${resp.status}）`, data?.field);
  }
  return data as T;
}

// ---- 通用 ----
export interface GeneralSettings {
  version: string;
  dataDir: string;
  baseDomain: string;
  baseDomainModified: boolean;
  apiKeySet: boolean;
  apiKeyMasked: string;
}
export interface UpstreamUpdate {
  baseDomain?: string;
  apiKey?: string;
  clearApiKey?: boolean;
}
export const getGeneral = () => request<GeneralSettings>('GET', '/general');
export const updateUpstream = (u: UpstreamUpdate) => request<GeneralSettings>('PUT', '/upstream', u);
export const restoreBaseDomain = () => request<GeneralSettings>('POST', '/upstream/base-domain/restore-default');
export const checkDataDir = (path: string) => request<{ path: string }>('POST', '/data-dir/check', { path });

// ---- 模型中心 ----
export type UISchema = Record<string, { label?: string; default?: string | number; options?: { label?: string; value: string | number }[] }>;
export interface GenControl {
  concurrency?: number;
  modules?: Record<string, { maxRetries?: number; retryIntervalSec?: number }>;
  inputLimits?: Record<string, { min?: number | null; max?: number | null }>;
  inputDuration?: unknown;
}
export interface ModelView {
  id: number;
  type: string;
  name: string;
  modelId: string;
  enabled: boolean;
  hidden: boolean;
  sortOrder: number;
  basePath: string;
  imageInputFormat: string;
  imageRequestMode: string;
  requestTemplate: unknown;
  pollingTemplate: unknown;
  uiSchema: UISchema | null;
  genControl: GenControl | null;
  modifiedFields: string[];
  references: string[];
}
export interface ModelPatch {
  enabled?: boolean;
  hidden?: boolean;
  name?: string;
  sortOrder?: number;
  uiSchemaDefaults?: Record<string, string | number>;
  concurrency?: number;
  canvasMaxRetries?: number;
  canvasRetryIntervalSec?: number;
}
export const listModels = () => request<{ data: ModelView[] }>('GET', '/models').then((r) => r.data);
export const patchModel = (id: number, p: ModelPatch) => request<ModelView>('PATCH', `/models/${id}`, p);
export const restoreModel = (id: number) => request<ModelView>('POST', `/models/${id}/restore-default`);

// ---- 画布配置 ----
export type ConfigKey =
  | 'canvas_postprocess_config'
  | 'gen_image_prompt_presets'
  | 'canvas_multi_angle'
  | 'canvas_light_control'
  | 'canvas_script_config';
export interface ConfigView<T = unknown> {
  key: ConfigKey;
  value: T | null;
  modified: boolean;
}
export const getConfig = <T>(key: ConfigKey) => request<ConfigView<T>>('GET', `/configs/${key}`);
export const putConfig = <T>(key: ConfigKey, value: T) => request<ConfigView<T>>('PUT', `/configs/${key}`, { value });
export const restoreConfig = <T>(key: ConfigKey) => request<ConfigView<T>>('POST', `/configs/${key}/restore-default`);

// ---- Agent 模型 ----
export interface AgentModelItem {
  id: number;
  role: string;
  alias: string;
  modelName: string;
  modified: boolean;
}
export interface AgentModelsView {
  data: AgentModelItem[];
  candidates: { modelId: string; name: string }[];
}
export const getAgentModels = () => request<AgentModelsView>('GET', '/agent-models');
export const switchAgentModel = (id: number, modelName: string) => request<AgentModelsView>('PUT', `/agent-models/${id}`, { modelName });
export const restoreAgentModel = (id: number) => request<AgentModelsView>('POST', `/agent-models/${id}/restore-default`);

// ---- Tauri 壳 ----
export interface DataDirStatus {
  current: string;
  defaultDir: string;
  lastError: string | null;
}
export const dataDirStatus = () => invoke<DataDirStatus>('data_dir_status');
/** 写入待迁移目录并重启应用；成功时应用会直接重启，不会返回。 */
export const applyDataDirChange = (path: string) => invoke<void>('apply_data_dir_change', { path });
export const dismissDataDirError = () => invoke<void>('dismiss_data_dir_error');
export const appVersion = () => getVersion();
