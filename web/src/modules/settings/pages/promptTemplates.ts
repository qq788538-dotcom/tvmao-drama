export interface MultiAnglePreset { key: string; name: string; rotation: number; tilt: number; zoom: string; prompt: string; [k: string]: unknown }
export interface MultiAngleConfig { models: string[]; template: string; presets: MultiAnglePreset[]; [k: string]: unknown }
export interface LightControlPreset {
  key: string; name: string; thumbnail?: string; direction?: string; brightness?: number | null; lightColor?: string; referenceImage?: string; prompt: string; [k: string]: unknown;
}
export interface LightControlConfig { models: string[]; template: string; presets: LightControlPreset[]; [k: string]: unknown }

const ZOOM_LABELS: Record<string, string> = { closeup: '特写', medium: '中景', panorama: '全景' };

/** 与 sidecar 校验一致：留空 = 用内置模板；非空必须含占位符。 */
export function templateError(template: string, placeholder: string): string | null {
  if (!template.trim() || template.includes(placeholder)) return null;
  return `拼接模板必须保留 ${placeholder}（留空则使用内置模板）`;
}

export function describeAngle(p: MultiAnglePreset): string {
  return `水平 ${p.rotation}° · 俯仰 ${p.tilt}° · ${ZOOM_LABELS[p.zoom] ?? p.zoom}`;
}

export function describeLight(p: LightControlPreset): string {
  const parts: string[] = [];
  if (p.direction) parts.push(`方向 ${p.direction}`);
  if (typeof p.brightness === 'number') parts.push(`亮度 ${p.brightness}`);
  if (p.lightColor) parts.push(`颜色 ${p.lightColor}`);
  if (p.referenceImage) parts.push('带参考图');
  return parts.length ? parts.join(' · ') : '仅附加提示词';
}
