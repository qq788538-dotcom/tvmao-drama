import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

vi.mock('@/shared/frameCache', () => ({
  saveFrames: vi.fn().mockResolvedValue(undefined),
  loadFrames: vi.fn().mockResolvedValue(null),
  deleteFrames: vi.fn().mockResolvedValue(undefined),
}));
vi.mock('@/shared/api', async (orig) => ({
  ...(await orig<object>()),
  api: { post: vi.fn(() => new Promise(() => {})) },
}));
vi.mock('../batchQueries', () => ({ loadSeedanceStatus: vi.fn().mockResolvedValue(undefined) }));
// 播放器与工具条与抽帧无关，jsdom 里挂真播放器只会制造噪音。
vi.mock('./VideoPlayer', () => ({ VideoPlayer: () => null }));
vi.mock('./VideoActionsBar', () => ({ VideoActionsBar: () => null }));

import { VideoInputNode } from './VideoInputNode';
import { useCanvasStore } from '../store';

// 假 <video>：与浏览器一致——把 currentTime 设成当前值不触发 seeked。
function fakeVideo() {
  const el = {
    crossOrigin: '',
    preload: '',
    muted: false,
    duration: 1,
    videoWidth: 4,
    videoHeight: 4,
    _t: 0,
    onloadeddata: null as null | (() => void),
    onseeked: null as null | (() => void),
    onerror: null as null | (() => void),
    removeAttribute: vi.fn(),
    load: vi.fn(),
    set src(_v: string) {
      setTimeout(() => el.onloadeddata?.(), 0);
    },
    get currentTime() {
      return el._t;
    },
    set currentTime(v: number) {
      if (v === el._t) return;
      el._t = v;
      setTimeout(() => el.onseeked?.(), 0);
    },
  };
  return el;
}

const origCreate = document.createElement.bind(document);
function stubVideoCreation() {
  vi.spyOn(document, 'createElement').mockImplementation(((tag: string, opts?: ElementCreationOptions) =>
    tag === 'video' ? (fakeVideo() as unknown as HTMLElement) : origCreate(tag, opts)) as typeof document.createElement);
}
beforeEach(() => {
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({ drawImage: vi.fn() } as never);
  vi.spyOn(HTMLCanvasElement.prototype, 'toDataURL').mockReturnValue('data:image/jpeg;base64,AA');
  useCanvasStore.setState({
    nodes: [
      { id: 'v1', type: 'video-input', x: 0, y: 0, width: 580, height: 400, content: 'https://x/v.mp4', settings: {} },
    ],
    connections: [],
    selectedIds: new Set<string>(),
    readOnly: false,
  });
});
afterEach(() => vi.restoreAllMocks());

// 回归：首帧 t=0 时 currentTime 已是 0，赋值不触发 seeked，旧实现死等，「抽帧中」永远转圈。
it('自动抽帧在首帧不等 seeked，能跑完并释放 video', async () => {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <VideoInputNode id="v1" />
    </QueryClientProvider>,
  );
  // 挂载后再换 createElement：节点菜单的时长探测也会建 <video>，不归这里管。
  stubVideoCreation();
  fireEvent.click(screen.getByText('自动抽帧（2fps）'));
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });
  const settings = useCanvasStore.getState().nodes[0].settings as { frames?: unknown[]; extractingFrames?: boolean };
  expect(settings.extractingFrames).toBe(false);
  expect(settings.frames).toHaveLength(2);
});
