// 图片工具条「全景」的行为约定钉死在这里：点击 = 铺一个 gen-image 下游节点
// + 连线 + 预选 720_panorama 预设 + 模型取后台绑定列表第一个；绝不自动提交
// 生成。后台没配预设/没绑模型时不建节点，只提示。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ImageActionsBar } from './ImageActionsBar';
import { useCanvasStore } from '../store';
import { fetchGenImagePresets, type GenImagePresetsConfig } from '@/shared/api';
import { persistNodeById } from '../persistence/canvasMutations';

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

// multi_cam_9_panel 故意不放首位（sort 也不是最小）——九宫格下拉必须自己把
// 它提到第一个，其余保持后台顺序。
const presetsConfig: GenImagePresetsConfig = {
  models: ['image-model', 'second-model'],
  presets: [
    {
      key: '720_panorama',
      value: { name: '720全景', placeholder: '点击生成，直接将场景图像转为720全景图。', sort: 5 },
    },
    { key: 'three_view', value: { name: '角色三视图', sort: 10 } },
    { key: 'multi_cam_9_panel', value: { name: '多机位九宫格', sort: 20 } },
    { key: 'portrait_texture_adjust', value: { name: '人像质感调节', sort: 30 } },
  ],
};

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

async function clickPanorama() {
  const btn = await screen.findByRole('button', { name: /全景/ });
  // 等预设配置 query settle 再点，避免命中「加载中」分支。
  await waitFor(() => expect(fetchGenImagePresets).toHaveBeenCalled());
  await userEvent.click(btn);
}

describe('ImageActionsBar 全景', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchGenImagePresets).mockResolvedValue(presetsConfig);
    setStore();
  });

  it('点击铺 gen-image 下游节点：连线 + 预设 key + 后台绑定模型取第一个', async () => {
    renderBar();
    await clickPanorama();

    await waitFor(() => {
      const st = useCanvasStore.getState();
      const created = st.nodes.find((n) => n.type === 'gen-image');
      expect(created).toBeTruthy();
      expect(created!.settings).toMatchObject({
        modelId: 'image-model',
        promptPreset: '720_panorama',
      });
      // 连线方向：当前图片 → 新节点。
      expect(
        st.connections.some((c) => c.from === 'src-1' && c.to === created!.id),
      ).toBe(true);
      // 位于源节点右侧且留了明显间距（160px gap，addNode 中心放置语义）。
      expect(created!.x).toBe(100 + 300 + 160);
      // 新节点保持 idle——绝不自动提交生成。
      expect(created!.status).toBe('idle');
    });
    expect(persistNodeById).toHaveBeenCalledWith(
      useCanvasStore.getState().nodes.find((n) => n.type === 'gen-image')!.id,
    );
  });

  it('九宫格下拉列出后台预设且 multi_cam_9_panel 置顶，点击按该预设铺节点', async () => {
    renderBar();
    const gridBtn = await screen.findByRole('button', { name: /九宫格/ });
    await waitFor(() => expect(fetchGenImagePresets).toHaveBeenCalled());
    await userEvent.click(gridBtn);

    // 菜单顺序：多机位九宫格（置顶）→ 其余按后台 sort（720全景 5 → 角色三视图 10）。
    const names = ['多机位九宫格', '720全景', '角色三视图'];
    const items = await screen.findAllByRole('button', {
      name: new RegExp(names.join('|')),
    });
    // 工具条上的「全景」按钮 title 是「生成720全景图」不含「720全景」字样，
    // 这里匹配到的只有菜单里的三项。
    expect(items.map((b) => b.textContent)).toEqual(names);

    await userEvent.click(screen.getByRole('button', { name: '多机位九宫格' }));
    await waitFor(() => {
      const st = useCanvasStore.getState();
      const created = st.nodes.find((n) => n.type === 'gen-image');
      expect(created).toBeTruthy();
      expect(created!.settings).toMatchObject({
        modelId: 'image-model',
        promptPreset: 'multi_cam_9_panel',
      });
      expect(st.connections.some((c) => c.from === 'src-1' && c.to === created!.id)).toBe(true);
    });
    // 点完菜单收起。
    expect(screen.queryByRole('button', { name: '角色三视图' })).toBeNull();
  });

  it('后台没配任何预设时点九宫格不开菜单，给出提示', async () => {
    vi.mocked(fetchGenImagePresets).mockResolvedValue({ models: ['image-model'], presets: [] });
    renderBar();
    const gridBtn = await screen.findByRole('button', { name: /九宫格/ });
    await waitFor(() => expect(fetchGenImagePresets).toHaveBeenCalled());
    await userEvent.click(gridBtn);

    expect(await screen.findByText('后台未配置绘图预设')).toBeInTheDocument();
    expect(useCanvasStore.getState().nodes).toHaveLength(1);
  });

  it('后台没配 720_panorama 预设时不建节点，给出提示', async () => {
    vi.mocked(fetchGenImagePresets).mockResolvedValue({ models: ['image-model'], presets: [] });
    renderBar();
    await clickPanorama();

    expect(await screen.findByText('后台未配置 720全景 预设')).toBeInTheDocument();
    expect(useCanvasStore.getState().nodes).toHaveLength(1);
    expect(useCanvasStore.getState().connections).toHaveLength(0);
  });

  it('人像质感调节：点击铺 gen-image 下游节点挂 portrait_texture_adjust', async () => {
    renderBar();
    const btn = await screen.findByRole('button', { name: /人像质感调节/ });
    await waitFor(() => expect(fetchGenImagePresets).toHaveBeenCalled());
    await userEvent.click(btn);

    await waitFor(() => {
      const st = useCanvasStore.getState();
      const created = st.nodes.find((n) => n.type === 'gen-image');
      expect(created).toBeTruthy();
      expect(created!.settings).toMatchObject({
        modelId: 'image-model',
        promptPreset: 'portrait_texture_adjust',
      });
      expect(st.connections.some((c) => c.from === 'src-1' && c.to === created!.id)).toBe(true);
      expect(created!.status).toBe('idle'); // 绝不自动提交生成
    });
  });

  it('后台没配 portrait_texture_adjust 时不建节点，给出提示', async () => {
    vi.mocked(fetchGenImagePresets).mockResolvedValue({ models: ['image-model'], presets: [] });
    renderBar();
    const btn = await screen.findByRole('button', { name: /人像质感调节/ });
    await waitFor(() => expect(fetchGenImagePresets).toHaveBeenCalled());
    await userEvent.click(btn);

    expect(await screen.findByText('后台未配置 人像质感调节 预设')).toBeInTheDocument();
    expect(useCanvasStore.getState().nodes).toHaveLength(1);
  });

  it('连点两次生成两个节点且竖向错开不重叠', async () => {
    renderBar();
    await clickPanorama();
    await waitFor(() =>
      expect(useCanvasStore.getState().nodes.filter((n) => n.type === 'gen-image')).toHaveLength(1),
    );
    // 第二次点击时工具条已因选中态转移隐藏是产品行为（addNode 会选中新节点），
    // 这里直接把选中拨回源节点模拟用户重新选中。
    useCanvasStore.setState({ selectedIds: new Set(['src-1']) });
    await userEvent.click(await screen.findByRole('button', { name: /全景/ }));

    await waitFor(() => {
      const created = useCanvasStore.getState().nodes.filter((n) => n.type === 'gen-image');
      expect(created).toHaveLength(2);
      // 竖向排队：第二个至少错开一个节点高度，完全不重叠。
      expect(Math.abs(created[1].y - created[0].y)).toBeGreaterThanOrEqual(created[0].height);
    });
  });
});
