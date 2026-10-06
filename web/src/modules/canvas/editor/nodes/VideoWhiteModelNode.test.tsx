import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { VideoWhiteModelNode } from './TieredVideoNode';
import { useCanvasStore } from '../store';
import {
  currentProjectId,
  fetchCanvasPostprocessConfig,
  fetchCanvasPostprocessEstimate,
  submitCanvasUpscale,
  submitCanvasWhiteModel,
  type CanvasPostprocessConfig,
} from '@/shared/api';

// 转白模与转高清共用 TieredVideoNode，档位/预估/收起面板等共性由 VideoUpscaleNode.test.tsx 覆盖。
// 这里只钉「共用」带来的风险：白模节点必须读白模那组配置、打白模端点，不能串到转高清。

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    fetchCanvasPostprocessConfig: vi.fn(),
    fetchCanvasPostprocessEstimate: vi.fn(),
    submitCanvasUpscale: vi.fn(),
    submitCanvasWhiteModel: vi.fn(),
    pollCanvasTaskUntilDone: vi.fn().mockReturnValue(new Promise(() => {})),
    currentProjectId: vi.fn().mockReturnValue(42),
  };
});

// 两组配置都配上且档位不重合——读错一组，断言就会挂。
const bothConfigured: CanvasPostprocessConfig = {
  subtitleRemoval: [],
  upscaleModels: [{ modelId: 'upscale-hd', label: '标准高清', resolutions: [{ value: '4k', label: '4K 极清' }] }],
  whiteModelModel: 'white-clay',
  whiteModelResolutions: [
    { value: '720p', label: '720p (短边<720px)' },
    { value: '1080p', label: '1080p (短边<1080px)' },
  ],
};

function renderNode() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <VideoWhiteModelNode id="clay-1" />
    </QueryClientProvider>,
  );
}

function seedStore() {
  useCanvasStore.setState({
    nodes: [
      {
        id: 'src-1', type: 'gen-video', x: 0, y: 0, width: 280, height: 180,
        status: 'succeeded', content: 'https://example.test/source.mp4', settings: {},
      },
      {
        id: 'clay-1', type: 'video-whitemodel', x: 400, y: 0, width: 320, height: 380,
        status: 'idle', settings: {},
      },
    ],
    connections: [{ id: 'conn-1', from: 'src-1', to: 'clay-1', inputType: 'video' }],
    selectedIds: new Set(['clay-1']),
    view: { x: 0, y: 0, zoom: 1 },
    lightbox: null,
  });
}

describe('VideoWhiteModelNode', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchCanvasPostprocessConfig).mockResolvedValue(bothConfigured);
    vi.mocked(fetchCanvasPostprocessEstimate).mockResolvedValue({ credits: 30, durationMs: 9000 });
    vi.mocked(currentProjectId).mockReturnValue(42);
    seedStore();
  });

  it('档位来自 whiteModelResolutions（平铺列表），预估用白模模型', async () => {
    renderNode();

    expect(await screen.findByText('720p (短边<720px)')).toBeInTheDocument();
    expect(screen.getByText('1080p (短边<1080px)')).toBeInTheDocument();
    expect(screen.queryByText('4K 极清')).toBeNull();
    await waitFor(() =>
      expect(fetchCanvasPostprocessEstimate).toHaveBeenCalledWith({
        projectId: 42, sourceNodeId: 'src-1', modelId: 'white-clay', resolution: '720p',
      }),
    );
    // 多模型下拉只给转高清；转白模仍是单模型，不显示。
    expect(screen.queryByRole('button', { name: '选择转高清模型' })).toBeNull();
  });

  it('选档位后点「转白模」走白模端点，不打转高清端点', async () => {
    vi.mocked(submitCanvasWhiteModel).mockResolvedValue({ taskId: 888, status: 'processing' });
    renderNode();

    await userEvent.click(await screen.findByText('1080p (短边<1080px)'));
    const btn = screen.getByRole('button', { name: '转白模' });
    await waitFor(() => expect(btn).not.toBeDisabled());
    await userEvent.click(btn);

    await waitFor(() =>
      expect(submitCanvasWhiteModel).toHaveBeenCalledWith({
        projectId: 42, nodeId: 'clay-1', sourceNodeId: 'src-1', modelId: 'white-clay', resolution: '1080p',
      }),
    );
    expect(submitCanvasUpscale).not.toHaveBeenCalled();
    await waitFor(() => expect(useCanvasStore.getState().nodes.find((n) => n.id === 'clay-1')?.taskId).toBe(888));
  });

  it('只配了转高清、没配白模 → 「未配置白模模型」+ 按钮 disabled，且不问价', async () => {
    vi.mocked(fetchCanvasPostprocessConfig).mockResolvedValue({
      ...bothConfigured, whiteModelModel: '', whiteModelResolutions: [],
    });
    renderNode();

    expect(await screen.findByText('未配置白模模型')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '转白模' })).toBeDisabled();
    expect(fetchCanvasPostprocessEstimate).not.toHaveBeenCalled();
  });
});
