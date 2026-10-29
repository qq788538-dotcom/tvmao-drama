import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App as AntApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { HistoryPanel, resolveOutputKind } from './HistoryPanel';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import type { GenerationTask, ModelConfig } from '@/shared/api';

// bug 史：「擦除字幕」产物是视频但被判成图片（修法=后端写 outputKind，前端优先信它）；
// 之后音频任务又暴露判定是二元的——非视频一律当图片：标签「文→图」、<img> 加载
// 音频 URL 裂图、下载后缀 .png。resolveOutputKind 把判定改成三态，回退链不变：
// outputKind > modelType > URL 扩展名 > 图片，旧任务不回归。
describe('resolveOutputKind', () => {
  it('outputKind 优先——即使 modelType 缺失、URL 无扩展名/后缀矛盾', () => {
    expect(resolveOutputKind('video', undefined, 'https://x/gen/task-1-0')).toBe('video');
    expect(resolveOutputKind('audio', undefined, 'https://x/gen/task-1-0')).toBe('audio');
    expect(resolveOutputKind('image', undefined, 'https://x/a.mp4')).toBe('image');
  });

  it('无 outputKind 时回退 modelType（音频模型的旧任务不再被当图片）', () => {
    expect(resolveOutputKind(undefined, 'audio', 'https://x/gen/task-1-0')).toBe('audio');
    expect(resolveOutputKind(undefined, 'video', 'https://x/gen/task-1-0')).toBe('video');
    expect(resolveOutputKind(undefined, 'image', 'https://x/gen/task-1-0')).toBe('image');
  });

  it('都缺失时回退 URL 扩展名，认不出算图片（旧任务不回归）', () => {
    expect(resolveOutputKind(undefined, undefined, 'https://x/a.mp4')).toBe('video');
    expect(resolveOutputKind(undefined, undefined, 'https://x/a.mp3')).toBe('audio');
    expect(resolveOutputKind(undefined, undefined, 'https://x/a.png')).toBe('image');
    expect(resolveOutputKind(undefined, undefined, 'https://x/gen/task-1-0')).toBe('image');
  });
});

// -------- 组件级：预览 / 发送到画布 / 下载，同一根因不能只在缩略图判定处修 --------
//
// da968ee 只在 HistoryItem 渲染缩略图那一处接了 resolveIsVideo；同文件里
// handlePreview / handleSendToCanvas / collectUrlsFromTasks / 右键下载
// 还各自裸写了一遍 URL 扩展名正则。这里用一个「去字幕视频」任务
// （outputKind=video、modelId 不在 /models 列表即 modelType 缺失、
// 产物 URL 无扩展名）驱动真实组件，断言这几条路径都不再把它误判成图片。

const listTasksMock = vi.fn();
const listRuntimeModelsMock = vi.fn();
const deleteTaskMock = vi.fn();

vi.mock('@/shared/api', async () => {
  const actual = await vi.importActual<typeof import('@/shared/api')>('@/shared/api');
  return {
    ...actual,
    listTasks: (...args: Parameters<typeof listTasksMock>) => listTasksMock(...args),
    listRuntimeModels: (...args: Parameters<typeof listRuntimeModelsMock>) => listRuntimeModelsMock(...args),
    deleteTask: (...args: Parameters<typeof deleteTaskMock>) => deleteTaskMock(...args),
  };
});

const SUBTITLE_ERASE_URL = 'https://x.test/gen/task-42-0';

function subtitleEraseTask(): GenerationTask {
  return {
    id: 42,
    userId: 1,
    projectId: 1,
    modelId: 'sd2.0-subtitle-erase', // 隐藏模型，不进 /models 列表 → modelType 拿不到
    status: 'succeeded',
    input: { inputs: {} },
    output: {
      outputUrls: [SUBTITLE_ERASE_URL], // 落库成 gen/task-N-0，没有 .mp4 之类扩展名
      outputKind: 'video', // 后端权威判定：真实是视频
    },
    createdAt: '2026-07-27T00:00:00.000Z',
    updatedAt: '2026-07-27T00:00:05.000Z',
  };
}

function renderHistoryPanel() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <AntApp>
      <QueryClientProvider client={queryClient}>
        <HistoryPanel open projectId={1} onClose={() => {}} />
      </QueryClientProvider>
    </AntApp>,
  );
}

describe('HistoryPanel 去字幕视频路径', () => {
  beforeEach(() => {
    listTasksMock.mockReset();
    listRuntimeModelsMock.mockReset();
    deleteTaskMock.mockReset();
    listTasksMock.mockResolvedValue({ data: [subtitleEraseTask()], total: 1 });
    listRuntimeModelsMock.mockResolvedValue([] as ModelConfig[]); // 隐藏模型不在列表里
    // 重置 store：避免上一个用例遗留的节点/lightbox 状态串到下一个用例
    useCanvasStore.setState({ nodes: [], connections: [], lightbox: null, selectedIds: new Set() });
  });

  it('handlePreview：判定为视频，Lightbox 用 video 模式打开', async () => {
    renderHistoryPanel();

    const previewBtn = await screen.findByTitle('放大预览');
    fireEvent.click(previewBtn);

    await waitFor(() => {
      expect(useCanvasStore.getState().lightbox).toEqual({
        url: SUBTITLE_ERASE_URL,
        kind: 'video',
        title: 'sd2.0-subtitle-erase',
      });
    });
  });

  it('handleSendToCanvas：判定为视频，建 video-input 节点而不是 input-image', async () => {
    const { container } = renderHistoryPanel();

    const videoEl = await waitFor(() => {
      const el = container.querySelector('video');
      expect(el).toBeTruthy();
      return el as HTMLVideoElement;
    });

    fireEvent.contextMenu(videoEl, { clientX: 10, clientY: 10 });
    const sendItem = await screen.findByText('发送到画布');
    fireEvent.click(sendItem);

    await waitFor(() => {
      const nodes = useCanvasStore.getState().nodes;
      expect(nodes).toHaveLength(1);
      expect(nodes[0].type).toBe('video-input');
      expect(nodes[0].content).toBe(SUBTITLE_ERASE_URL);
    });
  });
});

const AUDIO_URL = 'https://x.test/gen/task-77-0';

function audioTask(): GenerationTask {
  return {
    id: 77,
    userId: 1,
    projectId: 1,
    modelId: 'doubao-seed-audio-1-0',
    status: 'succeeded',
    input: { inputs: { prompt: '你好，我是张飞' } },
    output: {
      outputUrls: [AUDIO_URL], // 无扩展名，判定只能靠 outputKind
      outputKind: 'audio',
    },
    createdAt: '2026-08-04T00:00:00.000Z',
    updatedAt: '2026-08-04T00:00:14.000Z',
  };
}

describe('HistoryPanel 音频任务', () => {
  beforeEach(() => {
    listTasksMock.mockReset();
    listRuntimeModelsMock.mockReset();
    deleteTaskMock.mockReset();
    listTasksMock.mockResolvedValue({ data: [audioTask()], total: 1 });
    listRuntimeModelsMock.mockResolvedValue([] as ModelConfig[]);
    useCanvasStore.setState({ nodes: [], connections: [], lightbox: null, selectedIds: new Set() });
  });

  it('类型标签是「文→音频」，媒体区渲染 audio 播放器而不是裂图', async () => {
    const { container } = renderHistoryPanel();
    expect(await screen.findByText('文→音频')).toBeInTheDocument();
    await waitFor(() => {
      expect(container.querySelector('audio')).toBeTruthy();
      expect(container.querySelector('img')).toBeNull();
    });
  });

  it('发送到画布建 input-audio 节点', async () => {
    const { container } = renderHistoryPanel();
    const audioEl = await waitFor(() => {
      const el = container.querySelector('audio');
      expect(el).toBeTruthy();
      return el as HTMLAudioElement;
    });

    fireEvent.contextMenu(audioEl, { clientX: 10, clientY: 10 });
    const sendItem = await screen.findByText('发送到画布');
    fireEvent.click(sendItem);

    await waitFor(() => {
      const nodes = useCanvasStore.getState().nodes;
      expect(nodes).toHaveLength(1);
      expect(nodes[0].type).toBe('input-audio');
      expect(nodes[0].content).toBe(AUDIO_URL);
    });
  });
});

// P17：面板一次最多 200 条，原来每条视频都是一个 <video preload=metadata>，打开就
// 同时发几百个媒体请求。改成进入可视区才挂媒体；拖拽/右键/双击挂在外壳上，
// 没挂媒体时也能用。
describe('HistoryPanel 媒体懒挂载', () => {
  let observers: { cb: IntersectionObserverCallback; el?: Element; disconnected: boolean }[] = [];
  const origIO = (globalThis as { IntersectionObserver?: unknown }).IntersectionObserver;

  beforeEach(() => {
    observers = [];
    (globalThis as { IntersectionObserver?: unknown }).IntersectionObserver = class {
      rec: (typeof observers)[number];
      constructor(cb: IntersectionObserverCallback) {
        this.rec = { cb, disconnected: false };
        observers.push(this.rec);
      }
      observe(el: Element) {
        this.rec.el = el;
      }
      disconnect() {
        this.rec.disconnected = true;
      }
    };
    listTasksMock.mockReset();
    listRuntimeModelsMock.mockReset();
    listTasksMock.mockResolvedValue({ data: [subtitleEraseTask()], total: 1 });
    listRuntimeModelsMock.mockResolvedValue([] as ModelConfig[]);
    useCanvasStore.setState({ nodes: [], connections: [], lightbox: null, selectedIds: new Set() });
  });

  afterEach(() => {
    (globalThis as { IntersectionObserver?: unknown }).IntersectionObserver = origIO;
  });

  async function mediaShell() {
    await screen.findByTitle('放大预览');
    const rec = observers.find((o) => o.el && !o.disconnected);
    expect(rec).toBeTruthy();
    return rec!;
  }

  it('进入可视区前不挂 <video>，进入后挂上并停止观察', async () => {
    const { container } = renderHistoryPanel();
    const rec = await mediaShell();
    expect(container.querySelector('video')).toBeNull();
    act(() => {
      rec.cb([{ isIntersecting: true, target: rec.el! } as IntersectionObserverEntry], {} as IntersectionObserver);
    });
    expect(container.querySelector('video')?.getAttribute('src')).toBe(SUBTITLE_ERASE_URL);
    expect(rec.disconnected).toBe(true);
  });

  it('媒体没挂上时，外壳上右键照样能发送到画布、双击照样预览', async () => {
    renderHistoryPanel();
    const rec = await mediaShell();
    fireEvent.contextMenu(rec.el!, { clientX: 10, clientY: 10 });
    fireEvent.click(await screen.findByText('发送到画布'));
    await waitFor(() => expect(useCanvasStore.getState().nodes[0]?.type).toBe('video-input'));

    fireEvent.doubleClick(rec.el!);
    expect(useCanvasStore.getState().lightbox).toMatchObject({ url: SUBTITLE_ERASE_URL, kind: 'video' });
  });

  it('外壳可拖，拖拽数据是原图 url', async () => {
    renderHistoryPanel();
    const rec = await mediaShell();
    expect(rec.el!.getAttribute('draggable')).toBe('true');
    const data: Record<string, string> = {};
    fireEvent.dragStart(rec.el!, {
      dataTransfer: { setData: (k: string, v: string) => void (data[k] = v), effectAllowed: '' },
    });
    expect(data['text/uri-list']).toBe(SUBTITLE_ERASE_URL);
  });
});
