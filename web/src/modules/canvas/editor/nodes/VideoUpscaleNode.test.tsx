import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { VideoUpscaleNode } from './TieredVideoNode';
import { useCanvasStore } from '../store';
import {
  currentProjectId,
  fetchCanvasPostprocessConfig,
  submitCanvasUpscale,
  type CanvasPostprocessConfig,
} from '@/shared/api';

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    fetchCanvasPostprocessConfig: vi.fn(),
    submitCanvasUpscale: vi.fn(),
    pollCanvasTaskUntilDone: vi.fn().mockReturnValue(new Promise(() => {})),
    currentProjectId: vi.fn().mockReturnValue(42),
  };
});

const sourceVideoUrl = 'https://example.test/source.mp4';

// 两个模型：有同值档 4k，也各有独有档（2k 只在 hd，1080p 只在 pro）——用来钉「换模型时档位怎么处理」。
const configured: CanvasPostprocessConfig = {
  subtitleRemoval: [],
  upscaleModels: [
    {
      modelId: 'upscale-hd',
      label: '标准高清',
      resolutions: [
        { value: '2k', label: '2K 超清' },
        { value: '4k', label: '4K 极清' },
      ],
    },
    {
      modelId: 'upscale-pro',
      label: '专业高清',
      resolutions: [
        { value: '1080p', label: '1080P' },
        { value: '4k', label: '4K 专业' },
      ],
    },
  ],
  whiteModelModel: '',
  whiteModelResolutions: [],
};

const singleModel: CanvasPostprocessConfig = {
  subtitleRemoval: [],
  upscaleModels: [configured.upscaleModels[0]],
  whiteModelModel: '',
  whiteModelResolutions: [],
};

// 后台没配转高清模型时的配置——节点该显示「未配置转高清模型」并禁用提交。
const unconfigured: CanvasPostprocessConfig = {
  subtitleRemoval: [],
  upscaleModels: [],
  whiteModelModel: '',
  whiteModelResolutions: [],
};

// 打开模型下拉并选中某个模型。
async function pickModel(label: string) {
  await userEvent.click(await screen.findByRole('button', { name: '选择转高清模型' }));
  await userEvent.click(screen.getByRole('option', { name: label }));
}

function renderNode(id = 'up-1') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <VideoUpscaleNode id={id} />
    </QueryClientProvider>,
  );
}

// 建 src(gen-video) --video--> up-1(video-upscale) 的连线。
function seedStoreWithSource() {
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
        settings: {},
      },
      {
        id: 'up-1',
        type: 'video-upscale',
        x: 400,
        y: 0,
        width: 320,
        height: 260,
        status: 'idle',
        settings: {},
      },
    ],
    connections: [{ id: 'conn-1', from: 'src-1', to: 'up-1', inputType: 'video' }],
    selectedIds: new Set(['up-1']),
    view: { x: 0, y: 0, zoom: 1 },
    lightbox: null,
  });
}

function seedStoreNoSource() {
  useCanvasStore.setState({
    nodes: [
      {
        id: 'up-1',
        type: 'video-upscale',
        x: 0,
        y: 0,
        width: 320,
        height: 260,
        status: 'idle',
        settings: {},
      },
    ],
    connections: [],
    selectedIds: new Set(['up-1']),
    view: { x: 0, y: 0, zoom: 1 },
    lightbox: null,
  });
}

describe('VideoUpscaleNode', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchCanvasPostprocessConfig).mockResolvedValue(configured);
    vi.mocked(currentProjectId).mockReturnValue(42);
  });

  it('A. 默认第一个模型，档位来自该模型下发的 resolutions，默认选中第一档', async () => {
    seedStoreWithSource();
    renderNode();

    expect(await screen.findByText('2K 超清')).toBeInTheDocument();
    expect(screen.getByText('4K 极清')).toBeInTheDocument();
  });

  it('B. 不显示积分预估（X3）', async () => {
    seedStoreWithSource();
    renderNode();

    await screen.findByText('2K 超清');
    await waitFor(() => expect(screen.getByRole('button', { name: '转高清' })).not.toBeDisabled());
    expect(screen.queryByText(/积分|计算中|预计/)).toBeNull();
  });

  it('C. 换档位后提交用新档位', async () => {
    seedStoreWithSource();
    vi.mocked(submitCanvasUpscale).mockResolvedValue({ taskId: 778, status: 'processing' });
    renderNode();

    await userEvent.click(await screen.findByText('4K 极清'));
    const btn = screen.getByRole('button', { name: '转高清' });
    await waitFor(() => expect(btn).not.toBeDisabled());
    await userEvent.click(btn);

    await waitFor(() =>
      expect(submitCanvasUpscale).toHaveBeenCalledWith({
        projectId: 42,
        nodeId: 'up-1',
        sourceNodeId: 'src-1',
        modelId: 'upscale-hd',
        resolution: '4k',
      }),
    );
  });

  it('D. 点「转高清」提交，payload 含 modelId + 所选档位', async () => {
    seedStoreWithSource();
    vi.mocked(submitCanvasUpscale).mockResolvedValue({ taskId: 777, status: 'processing' });
    renderNode();

    await screen.findByText('2K 超清');
    const btn = await screen.findByRole('button', { name: '转高清' });
    await waitFor(() => expect(btn).not.toBeDisabled());

    await userEvent.click(btn);

    await waitFor(() =>
      expect(submitCanvasUpscale).toHaveBeenCalledWith({
        projectId: 42,
        nodeId: 'up-1',
        sourceNodeId: 'src-1',
        modelId: 'upscale-hd',
        resolution: '2k',
      }),
    );
  });

  it('E. 无上游视频 → 按钮 disabled + 「请连接一个视频」', async () => {
    seedStoreNoSource();
    renderNode();

    expect(screen.getByText('请连接一个视频')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '转高清' })).toBeDisabled();
  });

  it('F. 后台未配转高清模型 → 「未配置转高清模型」+ 按钮 disabled', async () => {
    seedStoreWithSource();
    vi.mocked(fetchCanvasPostprocessConfig).mockResolvedValue(unconfigured);
    renderNode();

    expect(await screen.findByText('未配置转高清模型')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '转高清' })).toBeDisabled();
  });

  it('G. 已有产物 → 收起档位面板（一次性操作，旧产物不被覆盖）', async () => {
    seedStoreWithSource();
    useCanvasStore.setState((s) => ({
      nodes: s.nodes.map((n) =>
        n.id === 'up-1'
          ? { ...n, content: 'https://example.test/hd.mp4', status: 'succeeded' as const }
          : n,
      ),
    }));
    renderNode();

    await waitFor(() => expect(screen.queryByRole('button', { name: '转高清' })).toBeNull());
    expect(screen.queryByText('2K 超清')).toBeNull();
    expect(screen.queryByRole('button', { name: '选择转高清模型' })).toBeNull();
  });

  it('H. 下拉框显示当前模型名，列出全部模型', async () => {
    seedStoreWithSource();
    renderNode();

    const trigger = await screen.findByRole('button', { name: '选择转高清模型' });
    expect(trigger).toHaveTextContent('标准高清');
    await userEvent.click(trigger);
    expect(screen.getByRole('option', { name: '标准高清' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('option', { name: '专业高清' })).toHaveAttribute('aria-selected', 'false');
  });

  it('I. 换模型且新模型有同值档位 → 保留该档位，按新模型提交', async () => {
    seedStoreWithSource();
    vi.mocked(submitCanvasUpscale).mockResolvedValue({ taskId: 777, status: 'processing' });
    renderNode();

    await userEvent.click(await screen.findByText('4K 极清'));
    await pickModel('专业高清');

    // 档位列表换成 pro 的，4k 仍选中（同 value 保留）。
    expect(await screen.findByText('1080P')).toBeInTheDocument();
    expect(screen.queryByText('2K 超清')).toBeNull();

    await userEvent.click(screen.getByRole('button', { name: '转高清' }));
    await waitFor(() =>
      expect(submitCanvasUpscale).toHaveBeenCalledWith({
        projectId: 42,
        nodeId: 'up-1',
        sourceNodeId: 'src-1',
        modelId: 'upscale-pro',
        resolution: '4k',
      }),
    );
  });

  it('J. 换模型且新模型没有当前档位 → 跳到新模型第一档', async () => {
    seedStoreWithSource();
    vi.mocked(submitCanvasUpscale).mockResolvedValue({ taskId: 779, status: 'processing' });
    renderNode();

    await screen.findByText('2K 超清'); // 默认 hd 的 2k，pro 没有 2k
    await pickModel('专业高清');
    expect(await screen.findByText('1080P')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '转高清' }));
    await waitFor(() =>
      expect(submitCanvasUpscale).toHaveBeenCalledWith({
        projectId: 42,
        nodeId: 'up-1',
        sourceNodeId: 'src-1',
        modelId: 'upscale-pro',
        resolution: '1080p',
      }),
    );
  });

  it('K. 后台只配一个模型也显示下拉框', async () => {
    seedStoreWithSource();
    vi.mocked(fetchCanvasPostprocessConfig).mockResolvedValue(singleModel);
    renderNode();

    expect(await screen.findByRole('button', { name: '选择转高清模型' })).toHaveTextContent('标准高清');
  });
});
