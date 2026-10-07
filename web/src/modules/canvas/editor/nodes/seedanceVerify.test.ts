import { QueryClient } from '@tanstack/react-query';
import { describe, expect, it, vi } from 'vitest';

const verifyMock = vi.fn();
vi.mock('@/shared/api', () => ({
  verifySeedance: (...args: unknown[]) => verifyMock(...args),
}));

import { verifySeedanceAndRefresh } from './seedanceVerify';

// 终审 I2：重拍/续写里不等待结果的校验调用，结束后（无论成败）都要让节点状态刷新。
describe('verifySeedanceAndRefresh', () => {
  it('失败时也刷新该素材的状态查询，并把错误抛回调用方', async () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, 'invalidateQueries');
    verifyMock.mockRejectedValueOnce(new Error('素材不合规'));
    await expect(verifySeedanceAndRefresh(qc, 'http://x.mp4', 'video')).rejects.toThrow('素材不合规');
    expect(spy).toHaveBeenCalledWith({ queryKey: ['seedance-status', 'http://x.mp4'] });
  });

  it('成功时同样刷新', async () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, 'invalidateQueries');
    verifyMock.mockResolvedValueOnce({ objectKey: 'k', status: 'active', assetId: 'a' });
    await verifySeedanceAndRefresh(qc, 'http://x.mp4', 'video');
    expect(spy).toHaveBeenCalledWith({ queryKey: ['seedance-status', 'http://x.mp4'] });
  });

  it('请求进行中先把状态置为 pending，让角标显示校验中并开启轮询', async () => {
    const qc = new QueryClient();
    let finish!: (v: unknown) => void;
    verifyMock.mockReturnValueOnce(new Promise((r) => (finish = r)));
    const p = verifySeedanceAndRefresh(qc, 'http://x.mp4', 'video');
    expect(qc.getQueryData<{ status: string }>(['seedance-status', 'http://x.mp4'])?.status).toBe('pending');
    finish({ objectKey: 'k', status: 'active', assetId: 'a' });
    await p;
  });

  it('已通过的素材不覆盖成 pending', async () => {
    const qc = new QueryClient();
    qc.setQueryData(['seedance-status', 'http://x.mp4'], { objectKey: 'k', status: 'active', assetId: 'a' });
    let finish!: (v: unknown) => void;
    verifyMock.mockReturnValueOnce(new Promise((r) => (finish = r)));
    const p = verifySeedanceAndRefresh(qc, 'http://x.mp4', 'video');
    expect(qc.getQueryData<{ status: string }>(['seedance-status', 'http://x.mp4'])?.status).toBe('active');
    finish({ objectKey: 'k', status: 'active', assetId: 'a' });
    await p;
  });
});
