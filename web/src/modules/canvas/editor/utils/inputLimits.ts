import type { ModelInputLimits } from '@/shared/api';

const KIND_LABEL = { video: '视频', image: '图片', audio: '音频' } as const;

/**
 * 按模型 gen_control.inputLimits 校验连线数量，返回违约文案；null = 通过。
 * 文案与后端 generation/input_limits.go 三句逐字一致——按钮 tooltip 上看到的话
 * 和绕过前端直调 API 收到的 400 是同一句，用户不会看到两套说法。
 */
export function inputLimitViolation(
  limits: ModelInputLimits | null | undefined,
  counts: { video: number; image: number; audio: number },
): string | null {
  if (!limits) return null;
  for (const kind of ['video', 'image', 'audio'] as const) {
    const lim = limits[kind];
    if (!lim) continue;
    const n = counts[kind];
    const label = KIND_LABEL[kind];
    if (typeof lim.max === 'number' && n > lim.max) {
      return lim.max === 0
        ? `该模型不支持连接${label}`
        : `该模型最多支持连接 ${lim.max} 个${label}，当前连接了 ${n} 个`;
    }
    if (typeof lim.min === 'number' && n < lim.min) {
      return `该模型需要连接至少 ${lim.min} 个${label}`;
    }
  }
  return null;
}
