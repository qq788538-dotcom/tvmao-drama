import type { QueryClient } from '@tanstack/react-query';

// 设置页和画布共用全局 queryClient。设置改完后，画布侧这些缓存必须失效，
// 否则回到画布最多 60 秒（模型目录）/ 300 秒（各画布配置）还在用旧配置。
// key 名以画布侧的 useQuery 为准：
//   runtime-models              节点模型下拉（GenImage/GenVideo/GenAudio/HistoryPanel 等）
//   canvas-script-styles        剧本生成分镜（ScriptGenerator，含候选模型）
// 以及设置页内部互相引用的列表：模型的「被引用」标记。
// 绘图预设、多角度、打光、视频后处理已写死为内置值，设置页不再改它们，但画布运行时仍读取这些 key，失效清单保留。
export const SETTINGS_DEPENDENT_QUERY_KEYS: readonly (readonly string[])[] = [
  ['runtime-models'],
  ['gen-image-presets'],
  ['multi-angle-config'],
  ['light-control-config'],
  ['canvas-postprocess-config'],
  ['canvas-script-styles'],
  ['settings-models'],
];

/** 任何模型 / 画布配置 / Agent 模型保存或恢复默认成功后调用。 */
export function invalidateAfterSettingsChange(qc: QueryClient): void {
  for (const queryKey of SETTINGS_DEPENDENT_QUERY_KEYS) {
    void qc.invalidateQueries({ queryKey: [...queryKey] });
  }
}

// 设置页的查询保持原来独立 client 时的行为：不重试、切回窗口不自动刷新、每次进页面都现取。
export const SETTINGS_QUERY_KEYS: readonly string[] = [
  'settings-models',
  'settings-general',
  'settings-datadir',
  'settings-app-version',
];

export function applySettingsQueryDefaults(qc: QueryClient): void {
  for (const key of SETTINGS_QUERY_KEYS) {
    qc.setQueryDefaults([key], { retry: false, refetchOnWindowFocus: false, staleTime: 0 });
  }
}
