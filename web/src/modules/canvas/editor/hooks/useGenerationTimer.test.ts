import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useGenerationTimer } from './useGenerationTimer';

// 刷新页面后 hook 是重新挂载的：如果起点取挂载时刻，计时条会从 0 重来。
// 这组测试钉死「起点来自外部传入的提交时刻」，挂载时就该显示已过去的秒数。
describe('useGenerationTimer', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-07T10:00:30.000Z'));
    // jsdom 有 rAF 但不受 fake timers 管；用 setTimeout 模拟成可推进的帧。
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) =>
      setTimeout(() => cb(Date.now()), 16) as unknown as number,
    );
    vi.stubGlobal('cancelAnimationFrame', (h: number) => clearTimeout(h));
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it('startedAt 在过去时，首帧 elapsed 就从已过去的秒数起算', () => {
    const startedAt = Date.now() - 30_000;
    const { result } = renderHook(() => useGenerationTimer(true, startedAt));
    act(() => {
      vi.advanceTimersByTime(16);
    });
    expect(result.current.elapsed).toBeGreaterThanOrEqual(30);
  });

  it('生成结束时 finalDuration 以 startedAt 为起点', () => {
    const startedAt = Date.now() - 30_000;
    const { result, rerender } = renderHook(
      ({ gen }: { gen: boolean }) => useGenerationTimer(gen, startedAt),
      { initialProps: { gen: true } },
    );
    act(() => {
      vi.advanceTimersByTime(5_000);
    });
    rerender({ gen: false });
    expect(result.current.finalDuration).toBeCloseTo(35, 0);
  });

  it('未传 startedAt（存量快照）时回退到挂载时刻', () => {
    const { result } = renderHook(() => useGenerationTimer(true));
    act(() => {
      vi.advanceTimersByTime(16);
    });
    expect(result.current.elapsed).toBeLessThan(1);
  });
});
