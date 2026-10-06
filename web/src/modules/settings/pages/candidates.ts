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

export function scriptErrors(cfg: ScriptConfig): string[] {
  const errs: string[] = [];
  const seen = new Set<string>();
  cfg.modelCandidates.forEach((c, i) => {
    if (!c.modelId) errs.push(`第 ${i + 1} 个候选模型未选择`);
    else if (seen.has(c.modelId)) errs.push(`候选模型重复：${c.modelId}`);
    if (!String(c.displayName ?? '').trim()) errs.push(`第 ${i + 1} 个候选模型的展示名不能为空`);
    seen.add(c.modelId);
  });
  return errs;
}

export function moveItem<T>(arr: T[], from: number, to: number): T[] {
  if (to < 0 || to >= arr.length || from === to) return arr.slice();
  const copy = arr.slice();
  const [it] = copy.splice(from, 1);
  copy.splice(to, 0, it);
  return copy;
}
