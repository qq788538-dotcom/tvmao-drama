// Seedance 2.0 素材库对待校验素材的规格要求。数值抄自
// docs/Assets API 参考文档v2(加真人审核).docx 的 CreateAsset 一节。
//
// 前端只硬挡时长——它是唯一能在本地便宜且可靠读到的量（media element 的
// loadedmetadata）。其余规格（像素数/帧率/文件大小/格式）只在 ? 卡片里列出来给人看：
// 半套本地校验会给人「按钮能点就一定合规」的错觉，而权威判定始终在上游，
// 不合规会以 failed_permanent 回到节点角标上。
export const SEEDANCE_DURATION_MIN_SEC = 2;
// 上限按类型分:视频 2026-08-17 从 15s 放宽到 30s(用户确认上游已放宽),
// 音频维持 15s 不动。
export const SEEDANCE_DURATION_MAX_SEC: Record<'video' | 'audio', number> = {
  video: 30,
  audio: 15,
};

export type SeedanceMediaKind = 'image' | 'video' | 'audio';

// 各类素材的要求条目，用于 ? 悬浮卡片。label/value 分开是为了让卡片按截图那样
// 加粗标签、常规值。
export const SEEDANCE_REQUIREMENTS: Record<
  SeedanceMediaKind,
  { title: string; items: { label: string; value: string }[] }
> = {
  image: {
    title: '待校验图像要求',
    items: [
      { label: '格式', value: 'jpeg、png、webp、bmp、tiff、gif、heic/heif' },
      { label: '宽高比（宽/高）', value: '(0.4, 2.5)' },
      { label: '宽高长度（px）', value: '(300, 6000)' },
      { label: '大小', value: '单张图片小于 30 MB' },
    ],
  },
  video: {
    title: '待校验视频要求',
    items: [
      { label: '格式', value: 'mp4、mov' },
      { label: '分辨率', value: '480p、720p' },
      { label: '时长', value: '2 ~ 30 秒' },
      { label: '宽高比（宽/高）', value: '[0.4, 2.5]' },
      { label: '宽高长度（px）', value: '[300, 6000]' },
      { label: '总像素数', value: '[640×640, 834×1112]（宽×高的乘积）' },
      { label: '大小', value: '单个视频不超过 50 MB' },
      { label: '帧率（FPS）', value: '[24, 60]' },
    ],
  },
  audio: {
    title: '待校验音频要求',
    items: [
      { label: '格式', value: 'wav、mp3' },
      { label: '时长', value: '2 ~ 15 秒' },
      { label: '大小', value: '单个音频不超过 15 MB' },
    ],
  },
};

// checkSeedanceDuration 判定该时长能不能提交。
//   - 图片没有时长概念 → 恒可提交
//   - duration 为 null（还没探测到 / 读不出来）→ 放行，交上游判定。本地探测只是
//     体验优化，探测失败把用户卡死比误提交更糟。
export function checkSeedanceDuration(
  kind: SeedanceMediaKind,
  duration: number | null,
): { ok: boolean; reason?: string } {
  if (kind === 'image' || duration == null) return { ok: true };
  const max = SEEDANCE_DURATION_MAX_SEC[kind];
  if (duration < SEEDANCE_DURATION_MIN_SEC || duration > max) {
    return {
      ok: false,
      reason: `时长 ${duration.toFixed(1)}s 不在 ${SEEDANCE_DURATION_MIN_SEC}~${max} 秒范围内`,
    };
  }
  return { ok: true };
}
