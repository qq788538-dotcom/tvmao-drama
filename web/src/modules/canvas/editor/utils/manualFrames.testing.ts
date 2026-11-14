import { vi } from 'vitest';

// 测试专用：替换全局 requestAnimationFrame / cancelAnimationFrame，由测试手动推进
// 「下一帧」（runFrame），用来断言按帧合并的提交时机。用完调 vi.unstubAllGlobals()。
export function stubFrames() {
  const pending = new Map<number, FrameRequestCallback>();
  let seq = 0;
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
    seq += 1;
    pending.set(seq, cb);
    return seq;
  });
  vi.stubGlobal('cancelAnimationFrame', (h: number) => {
    pending.delete(h);
  });
  return {
    count: () => pending.size,
    runFrame: () => {
      const cbs = [...pending.values()];
      pending.clear();
      for (const cb of cbs) cb(performance.now());
    },
  };
}
