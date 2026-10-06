import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { isLimitedAvailable, SubtitleEraseNode } from './SubtitleEraseNode';
import { useCanvasStore } from '../store';
import {
  currentProjectId,
  fetchCanvasPostprocessConfig,
  submitCanvasDesub,
  type CanvasPostprocessConfig,
} from '@/shared/api';

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    fetchCanvasPostprocessConfig: vi.fn(),
    submitCanvasDesub: vi.fn(),
    pollCanvasTaskUntilDone: vi.fn().mockReturnValue(new Promise(() => {})),
    currentProjectId: vi.fn().mockReturnValue(42),
  };
});

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

  it('E. 不显示积分预估（X3）', async () => {
    seedStoreWithSource(undefined);
    renderNode();

    await screen.findByText('普通去字幕');
    await waitFor(() => expect(screen.getByRole('button', { name: '去字幕' })).not.toBeDisabled());
    expect(screen.queryByText(/积分|计算中|预计/)).toBeNull();
  });
});
