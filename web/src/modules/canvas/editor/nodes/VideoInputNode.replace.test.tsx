import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

vi.mock('@/shared/frameCache', () => ({
  saveFrames: vi.fn().mockResolvedValue(undefined),
  loadFrames: vi.fn().mockResolvedValue(null),
  deleteFrames: vi.fn().mockResolvedValue(undefined),
}));
vi.mock('../utils/uploadIntoNode', () => ({ uploadFileIntoNode: vi.fn() }));
vi.mock('../persistence/canvasMutations', async (orig) => ({
  ...(await orig<object>()),
  persistNodeById: vi.fn(),
}));
vi.mock('../batchQueries', () => ({ loadSeedanceStatus: vi.fn().mockResolvedValue(undefined) }));
// 播放器与工具条与更换视频无关，jsdom 里挂真播放器只会制造噪音。
vi.mock('./VideoPlayer', () => ({ VideoPlayer: () => null }));
vi.mock('./VideoActionsBar', () => ({ VideoActionsBar: () => null }));

import { VideoInputNode } from './VideoInputNode';
import { useCanvasStore } from '../store';
import { saveFrames } from '@/shared/frameCache';
import { persistNodeById } from '../persistence/canvasMutations';
import { uploadFileIntoNode } from '../utils/uploadIntoNode';

// 假 <video>：只记录探测元数据时用到的字段与释放调用。
function fakeVideo() {
  return {
    preload: '',
    src: '',
    duration: 3,
    videoWidth: 640,
    videoHeight: 360,
    onloadedmetadata: null as null | (() => void),
    onerror: null as null | (() => void),
    removeAttribute: vi.fn(),
    load: vi.fn(),
  };
}

let created: ReturnType<typeof fakeVideo>[] = [];
const origCreate = document.createElement.bind(document);
function stubVideoCreation() {
  vi.spyOn(document, 'createElement').mockImplementation(((tag: string, opts?: ElementCreationOptions) => {
    if (tag !== 'video') return origCreate(tag, opts);
    const v = fakeVideo();
    created.push(v);
    return v as unknown as HTMLElement;
  }) as typeof document.createElement);
}

const oldFrame = { url: 'data:image/jpeg;base64,OLD', time: 0 };

beforeEach(() => {
  created = [];
  vi.mocked(saveFrames).mockReset().mockResolvedValue(undefined);
  vi.mocked(persistNodeById).mockClear();
  // 每个用例重设：afterEach 的 restoreAllMocks 会把 vi.fn 的实现一并清掉。
  vi.mocked(uploadFileIntoNode).mockResolvedValue({ id: 7, url: 'https://x/new.mp4', mimeType: 'video/mp4' } as never);
  useCanvasStore.setState({
    nodes: [
      {
        id: 'v1', type: 'video-input', x: 0, y: 0, width: 580, height: 400, content: 'https://x/old.mp4',
        settings: { frames: [oldFrame], selectedKeyframes: [oldFrame] },
      },
    ],
    connections: [],
    selectedIds: new Set<string>(),
    readOnly: false,
  });
});
afterEach(() => vi.restoreAllMocks());

function renderNode() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <VideoInputNode id="v1" />
    </QueryClientProvider>,
  );
}

async function replaceVideo(container: HTMLElement) {
  const input = container.querySelector('input[type="file"]') as HTMLInputElement;
  const file = new File(['x'], 'new.mp4', { type: 'video/mp4' });
  await act(async () => {
    fireEvent.change(input, { target: { files: [file] } });
    await new Promise((r) => setTimeout(r, 0));
  });
}

// P8：更换视频后旧视频的帧不能挂在新视频下面，IndexedDB 缓存也要一起清。
it('更换视频成功后清空帧与缓存，探测完元数据释放临时 video', async () => {
  const { container } = renderNode();
  // 挂载后再换 createElement：节点菜单的时长探测也会建 <video>，不归这里管。
  stubVideoCreation();
  await replaceVideo(container);

  const settings = useCanvasStore.getState().nodes[0].settings as Record<string, unknown>;
  expect(settings.frames).toEqual([]);
  expect(settings.selectedKeyframes).toEqual([]);
  expect(saveFrames).toHaveBeenCalledWith('v1', [], []);

  const probe = created[0];
  expect(probe.src).toBe('https://x/new.mp4');
  act(() => probe.onloadedmetadata?.());
  expect((useCanvasStore.getState().nodes[0].settings as Record<string, unknown>).videoMeta)
    .toEqual({ duration: 3, w: 640, h: 360 });
  expect(persistNodeById).toHaveBeenCalledWith('v1');
  expect(probe.removeAttribute).toHaveBeenCalledWith('src');
  expect(probe.load).toHaveBeenCalled();
});

// P35：探测失败也要释放，不能等 GC。
it('临时 video 加载失败时同样释放', async () => {
  const { container } = renderNode();
  stubVideoCreation();
  await replaceVideo(container);

  const probe = created[0];
  probe.onerror?.();
  expect(probe.removeAttribute).toHaveBeenCalledWith('src');
  expect(probe.load).toHaveBeenCalled();
  expect(persistNodeById).not.toHaveBeenCalled();
});
