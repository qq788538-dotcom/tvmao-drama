import type { QueryClient } from '@tanstack/react-query';
import { verifySeedance, type SeedanceAssetState } from '@/shared/api';

// 节点合规角标读的查询键（useMediaNodeMenu），这里与它保持一致。
export const seedanceStatusKey = (url: string) => ['seedance-status', url] as const;

/**
 * 提交 Seedance 合规校验并让节点角标跟上结果。
 *
 * 校验是同步调用 seedance-asset，可能要几分钟；后端在调用期间写一条临时 pending。
 * 请求一发出就把本地状态置为 pending（已通过的不动），角标立刻显示「校验中」，
 * 节点的 5 秒轮询随之开启；结束后无论成败都刷新一次，失败的「不合规」角标
 * 立刻出现（终审 I2）。错误照常抛给调用方。
 */
export async function verifySeedanceAndRefresh(
  queryClient: QueryClient,
  url: string,
  kind: 'image' | 'video' | 'audio',
): Promise<SeedanceAssetState> {
  const key = seedanceStatusKey(url);
  const current = queryClient.getQueryData<SeedanceAssetState | null>(key);
  if (current?.status !== 'active') {
    queryClient.setQueryData<SeedanceAssetState>(key, {
      objectKey: current?.objectKey ?? '',
      status: 'pending',
    });
  }
  try {
    return await verifySeedance(url, kind);
  } finally {
    queryClient.invalidateQueries({ queryKey: key });
  }
}
