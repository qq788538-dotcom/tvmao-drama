import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { isLimitedAvailable, SubtitleEraseNode } from './SubtitleEraseNode';
import { useCanvasStore } from '../store';
import {
  currentProjectId,
  fetchCanvasPostprocessConfig,
  fetchCanvasPostprocessEstimate,
  submitCanvasDesub,
  type CanvasPostprocessConfig,
} from '@/shared/api';
import { loadEstimate } from '../batchQueries';

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    fetchCanvasPostprocessConfig: vi.fn(),
    fetchCanvasPostprocessEstimate: vi.fn(),
    submitCanvasDesub: vi.fn(),
    pollCanvasTaskUntilDone: vi.fn().mockReturnValue(new Promise(() => {})),
    currentProjectId: vi.fn().mockReturnValue(42),
  };
});

// loadEstimate 仍被 mock，但只为断言「本节点再也不走这条路」——预估已改由后端算。
vi.mock('../batchQueries', () => ({
  loadEstimate: vi.fn().mockResolvedValue(3),
}));

const sourceVideoUrl = 'https://example.test/source.mp4';

const desubConfig: CanvasPostprocessConfig = {
  subtitleRemoval: [
    { modelId: 'normal-model', label: '普通去字幕', subtitle: '标准通道', isTimeLimited: false },
    { modelId: 'limited-model', label: '限时去字幕', subtitle: '仅上游未过期可用', isTimeLimited: true },
  ],
  upscaleModels: [],
  whiteModelModel: '',
  whiteModelResolutions: [],
};

function renderNode(id = 'erase-1') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <SubtitleEraseNode id={id} />
    </QueryClientProvider>,
  );
}

// 建 src(gen-video) --video--> erase-1(subtitle-erase) 的连线，src.settings 可选带 upstreamExpire。
function seedStoreWithSource(upstreamExpire?: string) {
  useCanvasStore.setState({
    nodes: [
      {
        id: 'src-1',
        type: 'gen-video',
        x: 0,
        y: 0,
        width: 280,
        height: 180,
        status: 'succeeded',
        content: sourceVideoUrl,
        settings: upstreamExpire ? { upstreamExpire } : {},
      },
      {
        id: 'erase-1',
        type: 'subtitle-erase',
        x: 400,
        y: 0,
        width: 320,
        height: 260,
        status: 'idle',
        settings: {},
      },
    ],
    connections: [{ id: 'conn-1', from: 'src-1', to: 'erase-1', inputType: 'video' }],
    selectedIds: new Set(['erase-1']),
    view: { x: 0, y: 0, zoom: 1 },
    lightbox: null,
  });
}

function seedStoreNoSource() {
  useCanvasStore.setState({
    nodes: [
      {
        id: 'erase-1',
        type: 'subtitle-erase',
        x: 0,
        y: 0,
        width: 320,
        height: 260,
        status: 'idle',
        settings: {},
      },
    ],
    connections: [],
    selectedIds: new Set(['erase-1']),
    view: { x: 0, y: 0, zoom: 1 },
    lightbox: null,
  });
}

describe('isLimitedAvailable', () => {
  it('无 upstreamExpire → false', () => {
    expect(isLimitedAvailable(undefined, new Date('2026-07-27T00:00:00Z'))).toBe(false);
  });

  it('upstreamExpire 在未来 → true', () => {
    expect(
      isLimitedAvailable('2026-07-28T00:00:00Z', new Date('2026-07-27T00:00:00Z')),
    ).toBe(true);
  });

  it('upstreamExpire 已过去 → false', () => {
    expect(
      isLimitedAvailable('2026-07-26T00:00:00Z', new Date('2026-07-27T00:00:00Z')),
    ).toBe(false);
  });
});

describe('SubtitleEraseNode', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchCanvasPostprocessConfig).mockResolvedValue(desubConfig);
    vi.mocked(fetchCanvasPostprocessEstimate).mockResolvedValue({ credits: 3, durationMs: 3000 });
    vi.mocked(loadEstimate).mockResolvedValue(3);
    vi.mocked(currentProjectId).mockReturnValue(42);
  });

  it('A. 上游是 seedance 且 upstreamExpire 未来 → 模型列表含限时项', async () => {
    seedStoreWithSource('2999-01-01T00:00:00Z');
    renderNode();

    expect(await screen.findByText('限时去字幕')).toBeInTheDocument();
    expect(screen.getByText('普通去字幕')).toBeInTheDocument();
  });

  it('B1. 上游 upstreamExpire 已过期 → 不含限时项', async () => {
    seedStoreWithSource('2000-01-01T00:00:00Z');
    renderNode();

    await screen.findByText('普通去字幕');
    expect(screen.queryByText('限时去字幕')).toBeNull();
  });

  it('B2. 上游无 upstreamExpire → 不含限时项', async () => {
    seedStoreWithSource(undefined);
    renderNode();

    await screen.findByText('普通去字幕');
    expect(screen.queryByText('限时去字幕')).toBeNull();
  });

  it('C. 无上游视频连线 → 「去字幕」按钮 disabled + 文案「请连接一个视频」', async () => {
    seedStoreNoSource();
    renderNode();

    expect(screen.getByText('请连接一个视频')).toBeInTheDocument();
    const btn = screen.getByRole('button', { name: '去字幕' });
    expect(btn).toBeDisabled();
  });

  it('D. 选普通模型点「去字幕」→ submitCanvasDesub 被调用，payload 正确', async () => {
    // 上游无 upstreamExpire → 只有一个普通选项，自动选中，无需再点选。
    seedStoreWithSource(undefined);
    vi.mocked(submitCanvasDesub).mockResolvedValue({ taskId: 999, status: 'processing' });
    renderNode();

    await screen.findByText('普通去字幕');
    const btn = await screen.findByRole('button', { name: '去字幕' });
    await waitFor(() => expect(btn).not.toBeDisabled());

    await userEvent.click(btn);

    await waitFor(() =>
      expect(submitCanvasDesub).toHaveBeenCalledWith({
        projectId: 42,
        nodeId: 'erase-1',
        sourceNodeId: 'src-1',
        modelId: 'normal-model',
      }),
    );
  });

  it('E. 预估走后端权威时长——按 sourceNodeId 请求，不再经 /models/estimate 合批器', async () => {
    seedStoreWithSource(undefined);
    vi.mocked(fetchCanvasPostprocessEstimate).mockResolvedValue({
      credits: 138,
      durationMs: 137200,
    });
    renderNode();

    expect(await screen.findByText('预计 138 积分')).toBeInTheDocument();
    expect(fetchCanvasPostprocessEstimate).toHaveBeenCalledWith({
      projectId: 42,
      sourceNodeId: 'src-1',
      modelId: 'normal-model',
    });
    // 旧路径彻底断开：不再自己拼 inputs.duration 去问价。
    expect(loadEstimate).not.toHaveBeenCalled();
  });

  it('F. 回归：源视频容器 metadata 被篡改成 1 秒，显示的仍是后端算的真实预估', async () => {
    // 复现用户遇到的场景——把一个两分多钟的视频 metadata 改成 1 秒后上传，节点显示
    // 「预计 1 积分」而实际按真实时长结算。根因是前端读 <video>.duration（容器声明值）
    // 自己算预估。这里让 jsdom 的 <video> 上报被篡改的 1 秒并派发 loadedmetadata，
    // 后端返回权威的 137.2s → 138 积分；断言界面显示 138 而不是 1。
    seedStoreWithSource(undefined);
    vi.mocked(fetchCanvasPostprocessEstimate).mockResolvedValue({
      credits: 138,
      durationMs: 137200,
    });
    const created: HTMLVideoElement[] = [];
    const origCreate = document.createElement.bind(document);
    const spy = vi.spyOn(document, 'createElement').mockImplementation(((tag: string) => {
      const el = origCreate(tag);
      if (tag === 'video') created.push(el as HTMLVideoElement);
      return el;
    }) as typeof document.createElement);

    renderNode();
    await screen.findByText('普通去字幕');

    // 先坐实 spy 真抓到了 <video>——否则下面的循环空转，这条测试会变成假绿。
    expect(created.length).toBeGreaterThan(0);

    // 给渲染出来的 <video> 喂一个被篡改的时长并触发事件——若实现还在监听
    // onLoadedMetadata 取时长，预估就会变成 1 积分。
    for (const el of created) {
      Object.defineProperty(el, 'duration', { value: 1, configurable: true });
      el.dispatchEvent(new Event('loadedmetadata'));
    }

    expect(await screen.findByText('预计 138 积分')).toBeInTheDocument();
    expect(screen.queryByText('预计 1 积分')).toBeNull();
    spy.mockRestore();
  });

  it('G. 预估未返回前显示「计算中…」，失败显示「预计 --」', async () => {
    seedStoreWithSource(undefined);
    vi.mocked(fetchCanvasPostprocessEstimate).mockRejectedValue(new Error('无法获取视频时长'));
    renderNode();

    await screen.findByText('普通去字幕');
    // 无依据时宁可不显示数字——显示一个算不出来的「预计 0 积分」比不显示更糟。
    expect(await screen.findByText('预计 --')).toBeInTheDocument();
  });
});
