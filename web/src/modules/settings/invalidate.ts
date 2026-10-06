import type { QueryClient } from '@tanstack/react-query';

// 设置页和画布共用全局 queryClient（final-review I-3）。设置改完后，画布侧这些缓存必须失效，
// 否则回到画布最多 60 秒（模型目录）/ 300 秒（各画布配置）还在用旧配置。
// key 名以画布侧的 useQuery 为准：
//   runtime-models              节点模型下拉（GenImage/GenVideo/GenAudio/HistoryPanel 等）
//   gen-image-presets           绘图预设（GenImageNode、ImageActionsBar）
//   multi-angle-config          多角度（MultiAnglePanel、ImageActionsBar）
//   light-control-config        打光（LightPanel、ImageActionsBar）
//   canvas-postprocess-config   视频后处理（SubtitleEraseNode、TieredVideoNode）
//   canvas-script-styles        剧本生成分镜（ScriptGenerator，含候选模型）
// 以及设置页内部互相引用的列表：模型的「被引用」标记、Agent 模型的候选。
export const SETTINGS_DEPENDENT_QUERY_KEYS: readonly (readonly string[])[] = [
  ['runtime-models'],
  ['gen-image-presets'],
  ['multi-angle-config'],
  ['light-control-config'],
  ['canvas-postprocess-config'],
  ['canvas-script-styles'],
  ['settings-models'],
  ['settings-agent-models'],
];

/** 任何模型 / 画布配置 / Agent 模型保存或恢复默认成功后调用。 */
export function invalidateAfterSettingsChange(qc: QueryClient): void {
  for (const queryKey of SETTINGS_DEPENDENT_QUERY_KEYS) {
    void qc.invalidateQueries({ queryKey: [...queryKey] });
  }
}

// 设置页的查询保持原来独立 client 时的行为：不重试、切回窗口不自动刷新、每次进页面都现取。
export const SETTINGS_QUERY_KEYS: readonly string[] = [
  'settings-config',
  'settings-models',
  'settings-agent-models',
  'settings-general',
  'settings-datadir',
  'settings-app-version',
];

export function applySettingsQueryDefaults(qc: QueryClient): void {
  for (const key of SETTINGS_QUERY_KEYS) {
    qc.setQueryDefaults([key], { retry: false, refetchOnWindowFocus: false, staleTime: 0 });
  }
}
