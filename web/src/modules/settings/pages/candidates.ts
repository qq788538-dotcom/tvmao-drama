// 后处理与脚本配置的类型（与参考仓 canvasgen.CanvasPostprocessConfig、canvasscript.Config 一致）。
// 索引签名保证未知字段在草稿里原样保留。
export interface DesubBinding { modelId: string; label: string; subtitle?: string; isTimeLimited?: boolean; [k: string]: unknown }
export interface UpscaleBinding { modelId: string; label: string; [k: string]: unknown }
export interface PostprocessConfig {
  subtitleRemovalModels?: DesubBinding[];
  upscaleModels?: UpscaleBinding[];
  upscaleModel?: string;
  whiteModelModel?: string;
  [k: string]: unknown;
}
export interface ScriptCandidate { modelId: string; displayName: string; [k: string]: unknown }
export interface ScriptStyle { name: string; version: string; skillName: string; enabled: boolean; userTags?: string[]; [k: string]: unknown }
export interface ScriptConfig {
  modelCandidates: ScriptCandidate[];
  styles: ScriptStyle[];
  concurrency?: number;
  maxRetries?: number;
  [k: string]: unknown;
}

/** 转高清配置是新列表格式还是旧单字段格式。新列表非空时以列表为准（与参考仓 LoadCanvasPostprocessConfig 一致）。 */
export function upscaleMode(cfg: PostprocessConfig): 'list' | 'legacy' | 'none' {
  if (cfg.upscaleModels && cfg.upscaleModels.length > 0) return 'list';
  if (typeof cfg.upscaleModel === 'string') return 'legacy';
  return 'none';
}

/** 参考仓 canvasscript maxAttempts()：maxRetries 缺省 / 0 / 越界都按默认重试 1 次。所以界面缺省显示 1，且不允许保存 0。 */
export const DEFAULT_MAX_RETRIES = 1;

const isIntIn = (n: unknown, lo: number, hi: number) => typeof n === 'number' && Number.isInteger(n) && n >= lo && n <= hi;

/** 与 sidecar validateConfigValue / checkRequiredSelections 保持一致。 */
export function scriptErrors(cfg: ScriptConfig): string[] {
  const errs: string[] = [];
  const seen = new Set<string>();
  const cands = cfg.modelCandidates ?? [];
  if (cands.length === 0) errs.push('至少保留一个候选模型');
  cands.forEach((c, i) => {
    if (!String(c.modelId ?? '').trim()) errs.push(`第 ${i + 1} 个候选模型未选择`);
    else if (seen.has(c.modelId)) errs.push(`候选模型重复：${c.modelId}`);
    if (!String(c.displayName ?? '').trim()) errs.push(`第 ${i + 1} 个候选模型的展示名不能为空`);
    seen.add(c.modelId);
  });
  if (cfg.concurrency !== undefined && !isIntIn(cfg.concurrency, 1, 20)) errs.push('同时执行的任务数必须是 1 到 20 的整数');
  if (cfg.maxRetries !== undefined && !isIntIn(cfg.maxRetries, 1, 10)) errs.push('失败自动重跑次数必须是 1 到 10 的整数');
  return errs;
}

export function postprocessErrors(cfg: PostprocessConfig): string[] {
  const errs: string[] = [];
  (cfg.subtitleRemovalModels ?? []).forEach((b, i) => {
    if (!String(b.modelId ?? '').trim()) errs.push(`第 ${i + 1} 个去字幕选项没有选择模型`);
  });
  return errs;
}

const pick = (src: Record<string, unknown>, keys: string[]) => Object.fromEntries(keys.filter((k) => k in src).map((k) => [k, src[k]]));

/**
 * 脚本配置的提交值：以服务端当前值为底，只带入后端白名单允许改的字段
 * （候选条目的 displayName、concurrency、maxRetries）；条目增删、顺序、modelId 一律取原值。
 */
export function scriptPayload(orig: ScriptConfig, draft: ScriptConfig): ScriptConfig {
  const out: ScriptConfig = { ...orig, modelCandidates: (orig.modelCandidates ?? []).map((c, i) => ({ ...c, ...pick(draft.modelCandidates?.[i] ?? {}, ['displayName']) })) };
  return { ...out, ...pick(draft, ['concurrency', 'maxRetries']) };
}

/** 后处理配置的提交值：去字幕条目只带 label / subtitle / isTimeLimited，另外只有旧格式的 upscaleModel；其余取原值。 */
export function postprocessPayload(orig: PostprocessConfig, draft: PostprocessConfig): PostprocessConfig {
  const out: PostprocessConfig = { ...orig, ...pick(draft, ['upscaleModel']) };
  if (orig.subtitleRemovalModels) {
    out.subtitleRemovalModels = orig.subtitleRemovalModels.map((b, i) => ({ ...b, ...pick(draft.subtitleRemovalModels?.[i] ?? {}, ['label', 'subtitle', 'isTimeLimited']) }));
  }
  return out;
}
