import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { api } from '@/shared/api';
import { useThumbUrl, __resetThumbCacheForTest } from './thumbs';

vi.mock('@/shared/api', () => ({
  api: { post: vi.fn() },
}));

const postMock = vi.mocked(api.post);

describe('useThumbUrl', () => {
  beforeEach(() => {
    __resetThumbCacheForTest();
    postMock.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('同一批 mount 的多个 URL 合并成一个请求，并各自换到缩略图', async () => {
    postMock.mockResolvedValue({
      data: {
        thumbs: {
          'https://cdn.test/a.png': 'https://cdn.test/a.png?x-tos-process=w400',
          'https://cdn.test/b.png': 'https://cdn.test/b.png?x-tos-process=w400',
        },
      },
    });

    const a = renderHook(() => useThumbUrl('https://cdn.test/a.png'));
    const b = renderHook(() => useThumbUrl('https://cdn.test/b.png'));

    // pending 期间不给 src，避免浏览器先下原图再下缩略图。
    expect(a.result.current).toBeUndefined();

    await waitFor(() => {
      expect(a.result.current).toBe('https://cdn.test/a.png?x-tos-process=w400');
      expect(b.result.current).toBe('https://cdn.test/b.png?x-tos-process=w400');
    });
    expect(postMock).toHaveBeenCalledTimes(1);
    expect(postMock).toHaveBeenCalledWith('/media/thumbs', {
      urls: ['https://cdn.test/a.png', 'https://cdn.test/b.png'],
      maxWidth: 400,
    });
  });

  it('超过后端单批上限(500)时按批切片，多请求各自换取', async () => {
    // 大画布场景：一次 mount 的 URL 数超过后端 thumbBatchMax(500)。
    // 若整批发出，后端直接 400 "too many urls"，整块画布回退原图、缩略图全失效。
    const urls = Array.from({ length: 501 }, (_, i) => `https://cdn.test/n${i}.png`);
    postMock.mockImplementation(async (_url, body: any) => ({
      data: {
        thumbs: Object.fromEntries(
          body.urls.map((u: string) => [u, `${u}?x-tos-process=w400`]),
        ),
      },
    }));

    const hooks = urls.map((u) => renderHook(() => useThumbUrl(u)));

    await waitFor(() => {
      expect(hooks[0].result.current).toBe('https://cdn.test/n0.png?x-tos-process=w400');
      expect(hooks[500].result.current).toBe('https://cdn.test/n500.png?x-tos-process=w400');
    });

    // 501 张 → 切成两批(500 + 1)，每批都 ≤500。
    expect(postMock).toHaveBeenCalledTimes(2);
    for (const call of postMock.mock.calls) {
      expect((call[1] as any).urls.length).toBeLessThanOrEqual(500);
    }
  });

  it('换取失败回退原图，且缓存住不重试打爆后端', async () => {
    postMock.mockRejectedValue(new Error('boom'));

    const { result } = renderHook(() => useThumbUrl('https://cdn.test/a.png'));
    await waitFor(() => {
      expect(result.current).toBe('https://cdn.test/a.png');
    });

    // 再次 mount 同一 URL：直接命中缓存，不再发请求。
    const again = renderHook(() => useThumbUrl('https://cdn.test/a.png'));
    expect(again.result.current).toBe('https://cdn.test/a.png');
    await act(async () => {
      await new Promise((r) => setTimeout(r, 80));
    });
    expect(postMock).toHaveBeenCalledTimes(1);
  });

  it('data:/空 URL 不发请求、原样返回', async () => {
    const dataUrl = 'data:image/png;base64,xxx';
    const { result } = renderHook(() => useThumbUrl(dataUrl));
    expect(result.current).toBe(dataUrl);

    const empty = renderHook(() => useThumbUrl(undefined));
    expect(empty.result.current).toBeUndefined();

    await act(async () => {
      await new Promise((r) => setTimeout(r, 80));
    });
    expect(postMock).not.toHaveBeenCalled();
  });
});
