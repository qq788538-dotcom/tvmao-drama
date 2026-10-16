import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  WAVEFORM_BAR_COUNT,
  WaveformPlayer,
  formatClock,
  peaksFromChannel,
  placeholderPeaks,
} from './WaveformPlayer';
import { useCanvasStore } from '../store';

describe('formatClock', () => {
  it('formats seconds as m:ss', () => {
    expect(formatClock(0)).toBe('0:00');
    expect(formatClock(5.4)).toBe('0:05');
    expect(formatClock(38)).toBe('0:38');
    expect(formatClock(136)).toBe('2:16');
  });

  it('falls back to 0:00 on garbage', () => {
    expect(formatClock(NaN)).toBe('0:00');
    expect(formatClock(Infinity)).toBe('0:00');
    expect(formatClock(-3)).toBe('0:00');
  });
});

describe('placeholderPeaks', () => {
  it('is deterministic for the same seed', () => {
    expect(placeholderPeaks('https://x/a.mp3')).toEqual(placeholderPeaks('https://x/a.mp3'));
  });

  it('differs across seeds and stays in (0,1]', () => {
    const a = placeholderPeaks('a');
    const b = placeholderPeaks('b');
    expect(a).not.toEqual(b);
    expect(a).toHaveLength(WAVEFORM_BAR_COUNT);
    for (const v of a) {
      expect(v).toBeGreaterThan(0);
      expect(v).toBeLessThanOrEqual(1);
    }
  });
});

describe('peaksFromChannel', () => {
  it('normalizes bucket peaks so the loudest bar hits 1', () => {
    // 前半段振幅 0.5、后半段 0.25 → 归一后前半段柱子 = 1、后半段 = 0.5。
    const data = new Float32Array(WAVEFORM_BAR_COUNT * 100);
    data.fill(0.5, 0, data.length / 2);
    data.fill(0.25, data.length / 2);
    const peaks = peaksFromChannel(data);
    expect(peaks[0]).toBe(1);
    expect(peaks[peaks.length - 1]).toBe(0.5);
  });

  it('gives silence a small floor instead of zero-height bars', () => {
    const peaks = peaksFromChannel(new Float32Array(1000));
    for (const v of peaks) expect(v).toBeGreaterThan(0);
  });
});

describe('WaveformPlayer', () => {
  beforeEach(() => {
    useCanvasStore.setState({ audioEdit: null });
  });

  it('renders placeholder bars and unknown duration before any download', () => {
    render(<WaveformPlayer url="https://x/a.mp3" />);
    // +1 是常驻的播放头（进度 0 也渲染，见下面的抓握测试）。
    expect(screen.getByTestId('waveform').children).toHaveLength(WAVEFORM_BAR_COUNT + 1);
    expect(screen.getByText('0:00 / --:--')).toBeTruthy();
    expect(screen.getByRole('button', { name: '播放' })).toBeTruthy();
  });

  // 用户拍板 2026-08-18：只有按住播放头才能拖动改进度；波形其他位置不拦事件，
  // 点击/拖动走 NodeShell 的节点选中/拖动。
  it('waveform lets mousedown propagate (node click/drag), playhead does not', () => {
    const outerMouseDown = vi.fn();
    render(
      <div onMouseDown={outerMouseDown}>
        <WaveformPlayer url="https://x/a.mp3" />
      </div>,
    );
    fireEvent.mouseDown(screen.getByTestId('waveform'));
    expect(outerMouseDown).toHaveBeenCalledTimes(1);
    fireEvent.mouseDown(screen.getByTestId('playhead'));
    expect(outerMouseDown).toHaveBeenCalledTimes(1);
  });

  it('renders the playhead even at progress 0 so it can be grabbed from the start', () => {
    render(<WaveformPlayer url="https://x/a.mp3" />);
    expect(screen.getByTestId('playhead')).toBeTruthy();
  });

  it('renders the trim selection overlay when this node has an active trim session', () => {
    useCanvasStore.setState({
      audioEdit: { nodeId: 'n1', mode: 'trim', start: 10, end: 45, rate: 1, duration: 136 },
    });
    render(<WaveformPlayer url="https://x/a.mp3" nodeId="n1" />);
    expect(screen.getByTestId('trim-overlay')).toBeTruthy();
    expect(screen.getByTestId('trim-handle-left')).toBeTruthy();
    expect(screen.getByTestId('trim-handle-right')).toBeTruthy();
    expect(screen.getByText('35.0s')).toBeTruthy();
  });

  it('does not render the overlay for other nodes or without nodeId', () => {
    useCanvasStore.setState({
      audioEdit: { nodeId: 'n1', mode: 'trim', start: 10, end: 45, rate: 1, duration: 136 },
    });
    render(<WaveformPlayer url="https://x/a.mp3" nodeId="n2" />);
    render(<WaveformPlayer url="https://x/b.mp3" />);
    expect(screen.queryByTestId('trim-overlay')).toBeNull();
  });
});

describe('WaveformPlayer 卸载竞态', () => {
  // 回归：点播放后全量 fetch 还在路上时节点被删/换音频（key 重挂载），卸载清理
  // 先跑，fetch 回来后才 createObjectURL——这个 blob 永远没人 revoke，整段音频
  // 常驻内存。
  it('fetch 在卸载后返回时不再创建 blob URL', async () => {
    let resolveFetch!: (r: Response) => void;
    const fetchMock = vi.fn(() => new Promise<Response>((r) => (resolveFetch = r)));
    const origFetch = globalThis.fetch;
    const origCreate = URL.createObjectURL;
    globalThis.fetch = fetchMock as unknown as typeof fetch;
    const createMock = vi.fn(() => 'blob:leak');
    URL.createObjectURL = createMock;
    try {
      const { unmount } = render(<WaveformPlayer url="https://x/race.mp3" />);
      fireEvent.click(screen.getByLabelText('播放'));
      expect(fetchMock).toHaveBeenCalledTimes(1);
      unmount();
      resolveFetch(new Response(new Uint8Array([1, 2, 3]), { status: 200 }));
      await new Promise((r) => setTimeout(r, 0));
      await new Promise((r) => setTimeout(r, 0));
      expect(createMock).not.toHaveBeenCalled();
    } finally {
      globalThis.fetch = origFetch;
      URL.createObjectURL = origCreate;
    }
  });
});
