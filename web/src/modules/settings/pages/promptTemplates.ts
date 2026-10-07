/** 与 sidecar 一致：多角度、打光、绘图预设的 models 至少一项。 */
export function modelsError(models: string[] | undefined): string | null {
  return models && models.length > 0 ? null : '至少选择一个适用模型';
}
