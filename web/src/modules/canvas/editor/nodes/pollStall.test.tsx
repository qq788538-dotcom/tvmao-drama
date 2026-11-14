import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ComponentType } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  CanvasPollStalledError,
  CanvasTaskGoneError,
  listRuntimeModels,
  pollCanvasTaskUntilDone,
} from '@/shared/api';
import { useCanvasStore } from '../store';
import type { CanvasNode } from '../types';
import { GenAudioNode } from './GenAudioNode';
import { GenImageNode } from './GenImageNode';
import { GenVideoNode } from './GenVideoNode';
import { SubtitleEraseNode } from './SubtitleEraseNode';
import { TextNode } from './TextNode';
import { VideoUpscaleNode, VideoWhiteModelNode } from './TieredVideoNode';
import {
  clearPollStalled,
  markPollStalled,
  stallIfUnreachable,
  usePollStallStore,
} from './pollStall';

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    listRuntimeModels: vi.fn().mockResolvedValue([]),
    submitCanvasTask: vi.fn(),
    pollCanvasTaskUntilDone: vi.fn(),
    uploadAsset: vi.fn(),
  };
});

vi.mock('../batchQueries', () => ({
  loadSeedanceStatus: vi.fn().mockResolvedValue(undefined),
}));

vi.mock('../persistence/canvasMutations', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../persistence/canvasMutations')>();
  return { ...actual, persistNodeById: vi.fn() };
});
import { persistNodeById } from '../persistence/canvasMutations';

// P23:轮询连续够不着后端时不判失败——节点保持 generating、挂起轮询、显示
// 「连接中断,点击重新检查」;只有任务确实不存在(连续 404)才判死。

describe('pollStall 标记', () => {
  beforeEach(() => {
    usePollStallStore.setState({ stalled: new Set() });
  });

  it('按 taskId 标记与清除,重复操作不换引用', () => {
    markPollStalled(5);
    const first = usePollStallStore.getState().stalled;
    expect(first.has(5)).toBe(true);
    markPollStalled(5);
    expect(usePollStallStore.getState().stalled).toBe(first);
    clearPollStalled(5);
    expect(usePollStallStore.getState().stalled.has(5)).toBe(false);
  });

  it('只有「连接中断」才挂起,其他错误交还调用方判失败', () => {
    expect(stallIfUnreachable(6, new CanvasTaskGoneError())).toBe(false);
    expect(stallIfUnreachable(6, new Error('x'))).toBe(false);
    expect(usePollStallStore.getState().stalled.has(6)).toBe(false);
    expect(stallIfUnreachable(6, new CanvasPollStalledError())).toBe(true);
    expect(usePollStallStore.getState().stalled.has(6)).toBe(true);
  });
});

// 六个轮询调用方(轮询 catch 都过 stallIfUnreachable)逐一覆盖,免得漏接一个就回到「判失败」。
const cases: Array<[string, CanvasNode['type'], ComponentType<{ id: string }>]> = [
  ['GenVideoNode', 'gen-video', GenVideoNode],
  ['GenImageNode', 'gen-image', GenImageNode],
  ['GenAudioNode', 'gen-audio', GenAudioNode],
  ['TextNode', 'text-node', TextNode],
  ['SubtitleEraseNode', 'subtitle-erase', SubtitleEraseNode],
  ['VideoUpscaleNode', 'video-upscale', VideoUpscaleNode],
  ['VideoWhiteModelNode', 'video-whitemodel', VideoWhiteModelNode],
];

describe.each(cases)('%s 轮询挂起', (_name, type, Node) => {
  function seed() {
    useCanvasStore.setState({
      nodes: [{
        id: 'n1',
        type,
        x: 0,
        y: 0,
        width: 280,
        height: 180,
        status: 'generating',
        taskId: 77,
        settings: {},
      }],
      connections: [],
      selectedIds: new Set<string>(),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
      readOnly: false,
    });
  }

  function renderNode() {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(
      <QueryClientProvider client={qc}>
        <Node id="n1" />
      </QueryClientProvider>,
    );
  }

  const nodeState = () => useCanvasStore.getState().nodesById.get('n1');

  beforeEach(() => {
    vi.clearAllMocks();
    // clearAllMocks 不清 mock*Once 队列:上一个用例没消费完的结果会串到下一个节点。
    vi.mocked(pollCanvasTaskUntilDone).mockReset();
    vi.mocked(listRuntimeModels).mockResolvedValue([]);
    usePollStallStore.setState({ stalled: new Set() });
    seed();
  });

  it('连接中断:保持 generating 与 taskId,不落库,显示「点击重新检查」', async () => {
    vi.mocked(pollCanvasTaskUntilDone).mockRejectedValueOnce(new CanvasPollStalledError());
    renderNode();

    expect(await screen.findByTestId('poll-stalled-retry')).toBeTruthy();
    expect(screen.getByText(/连接中断，点击重新检查/)).toBeTruthy();
    expect(nodeState()?.status).toBe('generating');
    expect(nodeState()?.taskId).toBe(77);
    expect(nodeState()?.error).toBeUndefined();
    expect(persistNodeById).not.toHaveBeenCalled();
    // 挂起期间不自动重启主轮询(低频对账交给 useReconcileCanvasTask)。
    expect(pollCanvasTaskUntilDone).toHaveBeenCalledTimes(1);
  });

  it('点「重新检查」:清挂起并立刻重启轮询,拿到终态照常写回', async () => {
    vi.mocked(pollCanvasTaskUntilDone)
      .mockRejectedValueOnce(new CanvasPollStalledError())
      .mockResolvedValueOnce({ status: 'failed', error: '上游拒绝' });
    renderNode();

    const btn = await screen.findByTestId('poll-stalled-retry');
    await act(async () => {
      fireEvent.click(btn);
    });

    await waitFor(() => expect(nodeState()?.status).toBe('failed'));
    expect(pollCanvasTaskUntilDone).toHaveBeenCalledTimes(2);
    expect(nodeState()?.error).toBe('上游拒绝');
    expect(screen.queryByTestId('poll-stalled-retry')).toBeNull();
  });

  it('任务确实不存在(连续 404):判死并落库', async () => {
    vi.mocked(pollCanvasTaskUntilDone).mockRejectedValueOnce(new CanvasTaskGoneError());
    renderNode();

    await waitFor(() => expect(nodeState()?.status).toBe('failed'));
    expect(nodeState()?.taskId).toBeUndefined();
    expect(persistNodeById).toHaveBeenCalledWith('n1');
    expect(screen.queryByTestId('poll-stalled-retry')).toBeNull();
  });
});
