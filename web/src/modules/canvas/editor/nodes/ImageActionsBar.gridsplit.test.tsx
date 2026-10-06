// 宫格切分交互约定钉死在这里：下拉四档 + 自定义 hover 选择器；选完规格进入
// 模式（模式条 + 节点上网格逐格点选）；「创建分镜组」把 rows/cols/选中格
// 原样交给 executeGridSplit，零选中时不调用；「生成高清图片」是 libtv 付费
// 超分的占位——必须不可点且 hover 有「该功能未实现」提示（用户点名的行为）。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ImageActionsBar } from './ImageActionsBar';
import { useCanvasStore } from '../store';
import { fetchGenImagePresets } from '@/shared/api';
import { executeGridSplit } from './gridsplit/executeGridSplit';

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    api: { post: vi.fn() },
    fetchGenImagePresets: vi.fn(),
  };
});

vi.mock('../persistence/canvasMutations', () => ({
  persistNodeById: vi.fn(),
  scheduleNodePatch: vi.fn(),
  flushNodePatch: vi.fn(),
}));

vi.mock('./gridsplit/executeGridSplit', () => ({
  executeGridSplit: vi.fn(),
}));

function setStore() {
  useCanvasStore.setState({
    nodes: [
      {
        id: 'src-1',
        type: 'input-image' as const,
        x: 100,
        y: 200,
        width: 300,
        height: 240,
        status: 'succeeded' as const,
        content: 'https://example.test/ref.png',
        settings: {},
      },
    ],
    connections: [],
    selectedIds: new Set(['src-1']),
    view: { x: 0, y: 0, zoom: 1 },
    readOnly: false,
  });
}

function renderBar() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <AntApp>
        <ImageActionsBar nodeId="src-1" visible />
      </AntApp>
    </QueryClientProvider>,
  );
}

async function openSplitMenu() {
  await userEvent.click(await screen.findByRole('button', { name: /宫格切分/ }));
}

describe('ImageActionsBar 宫格切分', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchGenImagePresets).mockResolvedValue({ models: [], presets: [] });
    vi.mocked(executeGridSplit).mockResolvedValue({ created: 1, failed: 0 });
    setStore();
  });

  it('下拉出四个固定档 + 自定义', async () => {
    renderBar();
    await openSplitMenu();
    for (const label of ['4宫格 (2×2)', '9宫格 (3×3)', '16宫格 (4×4)', '25宫格 (5×5)']) {
      expect(screen.getByRole('button', { name: label })).toBeInTheDocument();
    }
    expect(screen.getByRole('button', { name: '自定义' })).toBeInTheDocument();
  });

  it('选 2×2 进入切分模式：模式条 + 4 个带行列号的格子 + 高清占位不可用', async () => {
    renderBar();
    await openSplitMenu();
    await userEvent.click(screen.getByRole('button', { name: '4宫格 (2×2)' }));

    expect(screen.getByText('请选择宫格进行操作')).toBeInTheDocument();
    for (const label of ['1-1', '1-2', '2-1', '2-2']) {
      expect(screen.getByRole('button', { name: label })).toBeInTheDocument();
    }
    // 「生成高清图片」是占位：hover 有原生 title 提示，点击不触发任何执行。
    const stub = screen.getByText('生成高清图片');
    expect(stub.closest('[title="该功能未实现"]')).not.toBeNull();
    await userEvent.click(stub);
    expect(executeGridSplit).not.toHaveBeenCalled();
  });

  it('点格子累计计数、再点反选；零选中时创建按钮不触发执行', async () => {
    renderBar();
    await openSplitMenu();
    await userEvent.click(screen.getByRole('button', { name: '4宫格 (2×2)' }));

    await userEvent.click(screen.getByRole('button', { name: '创建分镜组' }));
    expect(executeGridSplit).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole('button', { name: '1-1' }));
    expect(screen.getByText('已选 1 个宫格')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '2-2' }));
    expect(screen.getByText('已选 2 个宫格')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '1-1' }));
    expect(screen.getByText('已选 1 个宫格')).toBeInTheDocument();
  });

  it('创建分镜组把规格与选中格原样交给执行器，成功后退出模式', async () => {
    renderBar();
    await openSplitMenu();
    await userEvent.click(screen.getByRole('button', { name: '4宫格 (2×2)' }));
    await userEvent.click(screen.getByRole('button', { name: '1-2' }));
    await userEvent.click(screen.getByRole('button', { name: '2-1' }));
    await userEvent.click(screen.getByRole('button', { name: '创建分镜组' }));

    expect(executeGridSplit).toHaveBeenCalledTimes(1);
    const arg = vi.mocked(executeGridSplit).mock.calls[0][0];
    expect(arg.sourceId).toBe('src-1');
    expect(arg.url).toBe('https://example.test/ref.png');
    expect(arg.rows).toBe(2);
    expect(arg.cols).toBe(2);
    expect([...arg.selected].sort()).toEqual(['0-1', '1-0']);
    // 成功后模式退出：模式条消失、常规工具条回来。
    expect(await screen.findByRole('button', { name: /宫格切分/ })).toBeInTheDocument();
    expect(screen.queryByText('创建分镜组')).toBeNull();
  });

  it('✕ 退出模式回到常规工具条', async () => {
    renderBar();
    await openSplitMenu();
    await userEvent.click(screen.getByRole('button', { name: '9宫格 (3×3)' }));
    expect(screen.getByText('请选择宫格进行操作')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '退出宫格切分' }));
    expect(screen.queryByText('请选择宫格进行操作')).toBeNull();
    expect(screen.getByRole('button', { name: /宫格切分/ })).toBeInTheDocument();
  });

  it('自定义 hover 选择器：点 3×4 进入 3 行 4 列模式', async () => {
    renderBar();
    await openSplitMenu();
    await userEvent.hover(screen.getByRole('button', { name: '自定义' }));
    await userEvent.click(await screen.findByRole('button', { name: '3×4' }));
    // 3×4 = 12 个格子，右下角行列号 3-4。
    expect(screen.getByRole('button', { name: '3-4' })).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: /^\d-\d$/ })).toHaveLength(12);
  });

  // gridSplittingNodeId 是切分模式对外的唯一信号：gen-image 的 prompt 面板
  // （另一棵组件树）靠它在切分时让位。进入置本节点 id，✕/创建成功清空——
  // 任何一步漏同步，面板都会盖住网格或永远不回来。
  it('切分模式镜像进 store：进入置 id，✕ 与创建成功都清空', async () => {
    renderBar();
    await openSplitMenu();
    await userEvent.click(screen.getByRole('button', { name: '4宫格 (2×2)' }));
    expect(useCanvasStore.getState().gridSplittingNodeId).toBe('src-1');

    await userEvent.click(screen.getByRole('button', { name: '退出宫格切分' }));
    expect(useCanvasStore.getState().gridSplittingNodeId).toBeNull();

    await openSplitMenu();
    await userEvent.click(screen.getByRole('button', { name: '4宫格 (2×2)' }));
    expect(useCanvasStore.getState().gridSplittingNodeId).toBe('src-1');
    await userEvent.click(screen.getByRole('button', { name: '1-1' }));
    await userEvent.click(screen.getByRole('button', { name: '创建分镜组' }));
    expect(await screen.findByRole('button', { name: /宫格切分/ })).toBeInTheDocument();
    expect(useCanvasStore.getState().gridSplittingNodeId).toBeNull();
  });
});
