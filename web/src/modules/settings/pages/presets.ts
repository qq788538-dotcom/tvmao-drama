export interface PresetParamOption { key: string; name: string; prompt?: string; default?: boolean; [k: string]: unknown }
export interface PresetParam { key: string; name: string; options: PresetParamOption[]; [k: string]: unknown }
export interface PresetValue { name?: string; category?: string; placeholder?: string; prompt?: string; params?: PresetParam[]; [k: string]: unknown }
export interface GenImagePresetsConfig { models: string[]; presets: { key: string; value: PresetValue; [k: string]: unknown }[]; [k: string]: unknown }

export function groupByCategory(cfg: GenImagePresetsConfig) {
  const groups: { category: string; items: { index: number; preset: GenImagePresetsConfig['presets'][number] }[] }[] = [];
  cfg.presets.forEach((preset, index) => {
    const category = preset.value.category || '未分类';
    let g = groups.find((x) => x.category === category);
    if (!g) groups.push((g = { category, items: [] }));
    g.items.push({ index, preset });
  });
  return groups;
}

export function presetErrors(cfg: GenImagePresetsConfig): string[] {
  const errs: string[] = [];
  if (!cfg.models || cfg.models.length === 0) errs.push('至少选择一个绘图模型');
  for (const p of cfg.presets) {
    if (!String(p.value.prompt ?? '').trim()) errs.push(`「${p.value.name ?? p.key}」的提示词不能为空`);
  }
  return errs;
}
