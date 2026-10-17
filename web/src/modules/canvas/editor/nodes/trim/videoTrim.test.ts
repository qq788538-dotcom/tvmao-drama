import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  clampSelection,
  dragSelection,
  extractFilmstrip,
  moveSelection,
  resizeSelection,
  trimVideo,
  isTrimSupported,
  MIN_TRIM_DURATION,
} from './videoTrim';

// mediabunny 只在浏览器有 WebCodecs 时才真正干活,jsdom 里整个 mock 掉,
// 测的是我们封装层的行为:参数传递、结果组装、错误路径。
const conversionInit = vi.fn();
const executeMock = vi.fn();
const disposeMock = vi.fn();

vi.mock('mediabunny', () => {
  class Input {
    dispose() {
      disposeMock();
    }
    async getPrimaryVideoTrack() {
      return { id: 1 };
    }
    async computeDuration() {
      return 5;
    }
  }
  class UrlSource {
    constructor(public url: string) {}
  }
  class CanvasSink {
    async *canvasesAtTimestamps(timestamps: number[]) {
      for (const t of timestamps) {
        // 模拟中间某帧解不出来(yield null)
        if (t > 2 && t < 3) {
          yield null;
          continue;
        }
        yield { canvas: { width: 96, height: 56 }, timestamp: t, duration: 0.1 };
      }
    }
  }
  class BufferTarget {
    buffer: ArrayBuffer | null = null;
  }
  class Mp4OutputFormat {}
  class Output {
    target: BufferTarget;
    constructor(opts: { target: BufferTarget }) {
      this.target = opts.target;
    }
  }
  const Conversion = {
    init: async (opts: any) => {
      conversionInit(opts);
      const conv = {
        onProgress: undefined as undefined | ((p: number, t: number) => void),
        execute: async () => {
          executeMock();
          conv.onProgress?.(0.5, 1);
          conv.onProgress?.(1, 2);
          opts.output.target.buffer = new ArrayBuffer(8);
        },
      };
      return conv;
    },
  };
  return { ALL_FORMATS: [], Input, UrlSource, CanvasSink, BufferTarget, Mp4OutputFormat, Output, Conversion };
});

// canvasToDataURL 要 document.createElement('canvas') + 2d context,jsdom 没有
// 真 canvas 实现,拦掉 createElement('canvas') 给一个假对象。
let createElementSpy: { mockRestore: () => void };
beforeEach(() => {
  const realCreate = document.createElement.bind(document);
  createElementSpy = vi.spyOn(document, 'createElement').mockImplementation(((tag: string) => {
    if (tag === 'canvas') {
      return {
        width: 0,
        height: 0,
        getContext: () => ({ drawImage: () => {} }),
        toDataURL: () => 'data:image/jpeg;base64,FAKE',
      } as unknown as HTMLCanvasElement;
    }
    return realCreate(tag as keyof HTMLElementTagNameMap);
  }) as typeof document.createElement);
});
afterEach(() => {
  createElementSpy.mockRestore();
  conversionInit.mockClear();
  executeMock.mockClear();
  disposeMock.mockClear();
});

describe('clampSelection', () => {
  it('钳制在 [0, duration] 内', () => {
    expect(clampSelection(-1, 6, 5)).toEqual({ start: 0, end: 5 });
  });

  it('保证最小选区时长', () => {
    const { start, end } = clampSelection(2, 2.1, 5);
    expect(end - start).toBeGreaterThanOrEqual(MIN_TRIM_DURATION);
  });

  it('视频本身短于最小时长时不越界', () => {
    const { start, end } = clampSelection(0, 0.3, 0.3);
    expect(start).toBe(0);
    expect(end).toBe(0.3);
  });
});

describe('moveSelection', () => {
  it('保持时长整体平移', () => {
    expect(moveSelection({ start: 1, end: 3 }, 0.5, 5)).toEqual({ start: 1.5, end: 3.5 });
  });

  it('钳在边界:右移到头时长不变', () => {
    expect(moveSelection({ start: 1, end: 3 }, 10, 5)).toEqual({ start: 3, end: 5 });
    expect(moveSelection({ start: 1, end: 3 }, -10, 5)).toEqual({ start: 0, end: 2 });
  });
});

describe('resizeSelection', () => {
  it('两端对称扩展/收缩', () => {
    expect(resizeSelection({ start: 1, end: 3 }, 0.5, 5)).toEqual({ start: 0.5, end: 3.5 });
    expect(resizeSelection({ start: 1, end: 3 }, -0.5, 5)).toEqual({ start: 1.5, end: 2.5 });
  });

  it('收缩不短于最小时长、扩展不越界', () => {
    const shrunk = resizeSelection({ start: 2, end: 2.6 }, -1, 5);
    expect(shrunk.end - shrunk.start).toBeGreaterThanOrEqual(MIN_TRIM_DURATION);
    expect(resizeSelection({ start: 0.2, end: 4.8 }, 1, 5)).toEqual({ start: 0, end: 5 });
  });
});

describe('dragSelection 整数秒吸附', () => {
  it('snap=false 原样走钳制', () => {
    expect(dragSelection('left', { start: 0, end: 5 }, 0.65, 5, false).start).toBeCloseTo(0.65);
  });

  it('把手吸到最近整数秒', () => {
    expect(dragSelection('left', { start: 0, end: 5 }, 0.65, 5, true).start).toBe(1);
    expect(dragSelection('right', { start: 0, end: 5 }, -1.4, 5, true).end).toBe(4);
  });

  it('平移吸附起点并保持时长', () => {
    const r = dragSelection('move', { start: 0.5, end: 2.5 }, 0.7, 5, true);
    expect(r).toEqual({ start: 1, end: 3 });
  });

  it('吸出界的值仍被钳回合法范围', () => {
    // 4.97s 长的视频,右把手吸到 5 会越界 → 钳回 duration
    const r = dragSelection('right', { start: 0, end: 4 }, 0.6, 4.97, true);
    expect(r.end).toBeCloseTo(4.97);
  });
});

describe('extractFilmstrip', () => {
  it('返回时长与 count 张帧,解不出的帧是空串占位', async () => {
    const { duration, frames } = await extractFilmstrip('http://x.mp4', 10);
    expect(duration).toBe(5);
    expect(frames).toHaveLength(10);
    // (i+0.5)/10*5 ∈ (2,3) 的两帧(i=4,5)是 null → 空串
    expect(frames[4]).toBe('');
    expect(frames[5]).toBe('');
    expect(frames[0]).toBe('data:image/jpeg;base64,FAKE');
    expect(disposeMock).toHaveBeenCalledTimes(1);
  });
});

describe('trimVideo', () => {
  it('把 start/end 传给 Conversion,产出 mp4 Blob,进度回调透传', async () => {
    const progress: number[] = [];
    const blob = await trimVideo('http://x.mp4', 1.2, 3.4, (p) => progress.push(p));
    expect(conversionInit).toHaveBeenCalledWith(
      expect.objectContaining({ trim: { start: 1.2, end: 3.4 } }),
    );
    expect(executeMock).toHaveBeenCalled();
    expect(blob.type).toBe('video/mp4');
    expect(blob.size).toBe(8);
    expect(progress).toEqual([0.5, 1]);
    expect(disposeMock).toHaveBeenCalledTimes(1);
  });

  // 出错路径同样要释放 Input(解码器/在途请求不能等 GC)。
  it('转码失败时也 dispose Input', async () => {
    executeMock.mockImplementationOnce(() => {
      throw new Error('boom');
    });
    await expect(trimVideo('http://x.mp4', 0, 1)).rejects.toThrow('boom');
    expect(disposeMock).toHaveBeenCalledTimes(1);
  });
});

describe('isTrimSupported', () => {
  it('跟随 globalThis.VideoEncoder 的有无', () => {
    vi.stubGlobal('VideoEncoder', class {});
    expect(isTrimSupported()).toBe(true);
    vi.unstubAllGlobals();
    expect(isTrimSupported()).toBe(false);
  });
});
