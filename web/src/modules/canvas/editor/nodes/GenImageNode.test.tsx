import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { GenImageNode } from './GenImageNode';
import { StoryboardModeContext } from '../../storyboard/StoryboardContext';
import { beginNodeDrag, endNodeDrag } from '../../storyboard/dragConnect';
import { useCanvasStore } from '../store';
import {
  api,
  listRuntimeModels,
  pollCanvasTaskUntilDone,
  submitCanvasTask,
  type ModelConfig,
} from '@/shared/api';
import { loadSeedanceStatus } from '../batchQueries';

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    // thumbs.ts 走 api.post 换缩略图——stub 掉，否则 jsdom 里发真实 XHR。
    api: { post: vi.fn() },
    listRuntimeModels: vi.fn().mockResolvedValue([]),
    // 提示词预设配置默认为空（= 后台未配置），本文件的用例不涉及预设；
    // 不 stub 的话组件里的 useQuery 会在 jsdom 里发真实 XHR。
    // 预设专属用例在 GenImageNode.presets.test.tsx。
    fetchGenImagePresets: vi.fn().mockResolvedValue({ models: [], presets: [] }),
    submitCanvasTask: vi.fn(),
    pollCanvasTaskUntilDone: vi.fn(),
    uploadAsset: vi.fn(),
  };
});

// 合规状态走合批器（batchQueries.ts）——按节点边界 stub 掉，隔离合批器的
// 时间窗内部逻辑，让组件测试只关心「传了什么、几次」。
vi.mock('../batchQueries', () => ({
  loadSeedanceStatus: vi.fn().mockResolvedValue(undefined),
}));

const imageURL = 'https://example.test/generated.png';
// 节点内 <img> 展示的是缩略图 URL；原图 imageURL 只流向 Lightbox/下游。
const thumbURL = `${imageURL}?x-tos-process=w400`;

// 缩略图换取是异步批量的，首次渲染 img 无 src——等它换到缩略图再取元素。
async function findThumbImage(container: HTMLElement): Promise<Element> {
  return waitFor(() => {
    const image = container.querySelector(`img[src="${thumbURL}"]`);
    expect(image).not.toBeNull();
    return image!;
  });
}
const longImageModelName = 'imagen-ultra-realistic-v3.5-4k（高质量通道）';
const imageModel: ModelConfig = {
  id: 1,
  type: 'image',
  name: 'Image Model',
  modelId: 'image-model',
  basePath: '',
  enabled: true,
  hidden: false,
  sortOrder: 1,
  requestTemplate: {},
  uiSchema: {
    ratio: {
      default: '1:1',
      options: [{ value: '1:1', label: '1:1' }],
    },
    n: {
      default: 1,
      options: [{ value: 1, label: '1' }],
    },
  },
};

function renderNode() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <GenImageNode id="image-1" />
    </QueryClientProvider>,
  );
}

/** 故事板详情面板里的渲染态：同一个组件，包在 StoryboardModeContext 里。 */
function renderNodeInStoryboard() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <StoryboardModeContext.Provider value={true}>
        <GenImageNode id="image-1" />
      </StoryboardModeContext.Provider>
    </QueryClientProvider>,
  );
}

describe('GenImageNode lightbox', () => {
  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    vi.mocked(listRuntimeModels).mockResolvedValue([imageModel]);
    vi.mocked(loadSeedanceStatus).mockResolvedValue(undefined);
    vi.mocked(api.post).mockResolvedValue({
      data: { thumbs: { [imageURL]: thumbURL } },
    });
    useCanvasStore.setState({
      nodes: [{
        id: 'image-1',
        type: 'gen-image',
        x: 0,
        y: 0,
        width: 280,
        height: 280,
        status: 'succeeded',
        content: imageURL,
        settings: { history: [imageURL], historyIndex: 0, ratio: '1:1', n: 1 },
      }],
      connections: [],
      selectedIds: new Set(['other-node']),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  });

  it('selects the node when the current result is clicked', async () => {
    const { container } = renderNode();
    const image = await findThumbImage(container);

    await userEvent.click(image);

    expect([...useCanvasStore.getState().selectedIds]).toEqual(['image-1']);
  });

  // 右键菜单里的「创建副本」：所有用 useMediaNodeMenu 的节点(6 个)共用这一项。
  it('右键菜单的「创建副本」在旁边复制出内容相同的节点', async () => {
    const { container } = renderNode();
    const image = await findThumbImage(container);

    fireEvent.contextMenu(image);
    await userEvent.click(await screen.findByText('创建副本'));

    const nodes = useCanvasStore.getState().nodes;
    expect(nodes).toHaveLength(2);
    const dup = nodes.find((n) => n.id !== 'image-1')!;
    expect(dup.content).toBe(imageURL);
    expect(dup.settings).toEqual(nodes.find((n) => n.id === 'image-1')!.settings);
    // 副本成为唯一选中项,菜单关闭。
    expect([...useCanvasStore.getState().selectedIds]).toEqual([dup.id]);
    expect(screen.queryByText('创建副本')).toBeNull();
  });

  it('opens the shared image lightbox when the current result is double-clicked', async () => {
    const { container } = renderNode();
    const image = await findThumbImage(container);

    await userEvent.dblClick(image);

    // 展示走缩略图，但 Lightbox 必须拿到原图 URL——这是缩略图机制的核心不变量。
    expect(useCanvasStore.getState().lightbox).toEqual({
      url: imageURL,
      kind: 'image',
      title: 'AI 绘图',
    });
  });

  it('keeps node dragging available from the current result image', async () => {
    const { container } = renderNode();
    const image = await findThumbImage(container);

    fireEvent.mouseDown(image, { button: 0, clientX: 10, clientY: 10 });
    fireEvent.mouseMove(window, { clientX: 30, clientY: 35 });
    fireEvent.mouseUp(window);

    const node = useCanvasStore.getState().nodes.find((n) => n.id === 'image-1');
    expect(node).toMatchObject({ x: 20, y: 25 });
  });

  it('hides the upload button after an image result exists', () => {
    renderNode();

    expect(screen.queryByTitle('上传图片（节点将转为图片节点）')).toBeNull();
  });

  it('shows the upload button before an image result exists', () => {
    useCanvasStore.setState({
      nodes: [{
        id: 'image-1',
        type: 'gen-image',
        x: 0,
        y: 0,
        width: 280,
        height: 280,
        status: 'idle',
        settings: { ratio: '1:1', n: 1 },
      }],
      selectedIds: new Set(['image-1']),
    });

    renderNode();

    expect(screen.getByTitle('上传图片（节点将转为图片节点）')).toBeInTheDocument();
  });

  // 桌面端不计费。哪怕模型配置里带着 priceNote（单价说明），节点上也不出现
  // 任何估价/积分文案。
  it('选中后不显示估价或积分文案（X3）', async () => {
    vi.mocked(listRuntimeModels).mockResolvedValue([{ ...imageModel, priceNote: '100积分/分钟' }]);
    useCanvasStore.setState({ selectedIds: new Set(['image-1']) });
    renderNode();

    expect((await screen.findAllByText('Image Model', {}, { timeout: 3000 })).length).toBeGreaterThan(0);
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 700));
    });
    expect(screen.queryByText(/积分|估算中|预计/)).toBeNull();
  });

  it('shows the resolution option label on the pill, not the raw value', async () => {
    // 与 GenVideoNode 同源问题：分辨率档位 value 可能是内部 ID/尺寸串，
    // pill 上必须展示 label。
    vi.mocked(listRuntimeModels).mockResolvedValue([
      {
        ...imageModel,
        uiSchema: {
          ...imageModel.uiSchema,
          resolution: {
            default: 'internal-1k-variant',
            options: [
              { value: 'internal-1k-variant', label: '1K' },
              { value: 'internal-2k-variant', label: '2K' },
            ],
          },
        },
      },
    ]);
    useCanvasStore.setState({ selectedIds: new Set(['image-1']) });
    renderNode();

    expect(await screen.findByText('1:1 · 1K')).toBeInTheDocument();
    expect(screen.queryByText(/internal-1k-variant/)).toBeNull();
  });

  it('shows a wide responsive model menu without truncating long names', async () => {
    vi.mocked(listRuntimeModels).mockResolvedValue([
      { ...imageModel, name: longImageModelName },
    ]);
    useCanvasStore.setState({ selectedIds: new Set(['image-1']) });
    renderNode();

    const trigger = await screen.findByRole('button', { name: longImageModelName });
    await userEvent.click(trigger);

    const labels = screen.getAllByText(longImageModelName);
    const optionLabel = labels.find((label) => label.closest('button') !== trigger);
    expect(optionLabel).toBeDefined();
    expect(optionLabel).not.toHaveClass('truncate');
    expect(optionLabel).toHaveClass('whitespace-normal', 'break-words');
    expect(optionLabel!.closest('.absolute')).toHaveStyle({
      width: '420px',
      maxWidth: 'calc(100vw - 32px)',
    });
    expect(optionLabel!.closest('button')).toHaveClass('items-start');
  });

  it('切换模型时把参数重置为新模型的默认值', async () => {
    const modelB: ModelConfig = {
      ...imageModel,
      id: 2,
      name: 'Model B',
      modelId: 'model-b',
      uiSchema: {
        ratio: { default: '9:16', options: [{ value: '9:16', label: '9:16' }, { value: '1:1', label: '1:1' }] },
        n: { default: 1, options: [{ value: 1, label: '1' }] },
      },
    };
    vi.mocked(listRuntimeModels).mockResolvedValue([imageModel, modelB]);
    useCanvasStore.setState({
      nodes: [{
        id: 'image-1', type: 'gen-image', x: 0, y: 0, width: 280, height: 280,
        status: 'idle', settings: { modelId: 'image-model', ratio: '1:1', n: 1 },
      }],
      selectedIds: new Set(['image-1']),
    });
    renderNode();

    // 打开模型菜单并选 Model B。
    await userEvent.click(await screen.findByRole('button', { name: 'Image Model' }));
    await userEvent.click(await screen.findByText('Model B'));

    const s = useCanvasStore.getState().nodes.find((n) => n.id === 'image-1')!.settings!;
    expect(s.modelId).toBe('model-b');
    // ratio 从旧的 1:1 重置为 Model B 的默认 9:16。
    expect(s.ratio).toBe('9:16');
  });
});

// 每一版结果记住自己的提示词。第一条用例是硬约束：线上存量画布（history 是
// 纯 URL 数组）必须一个像素都不变——否则老用户一打开就发现节点长得不一样了。
describe('GenImageNode 版本提示词', () => {
  const urlA = 'https://example.test/v1.png';
  const urlB = 'https://example.test/v2.png';

  function setNode(settings: Record<string, unknown>, content: string) {
    useCanvasStore.setState({
      nodes: [{
        id: 'image-1',
        type: 'gen-image',
        x: 0,
        y: 0,
        width: 280,
        height: 280,
        status: 'succeeded',
        content,
        settings: { ratio: '1:1', n: 1, ...settings },
      }],
      connections: [],
      selectedIds: new Set(['image-1']),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  }

  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    vi.mocked(listRuntimeModels).mockResolvedValue([imageModel]);
    vi.mocked(loadSeedanceStatus).mockResolvedValue(undefined);
    vi.mocked(api.post).mockResolvedValue({ data: { thumbs: {} } });
  });

  const editor = () => screen.getByRole('textbox', { name: '生成提示词' });

  it('老快照(history 是纯 URL 数组)翻页时输入框内容不变——存量画布零变化', async () => {
    setNode({ history: [urlA, urlB], historyIndex: 1, prompt: '最新那句' }, urlB);
    renderNode();

    expect(editor()).toHaveTextContent('最新那句');

    // 翻回第 1 版：老条目没有自带 prompt，回退到 settings.prompt，输入框原地不动。
    await userEvent.click(screen.getByTitle('上一张'));
    expect(useCanvasStore.getState().nodes[0].settings!.historyIndex).toBe(0);
    expect(editor()).toHaveTextContent('最新那句');
    // content 仍然正确镜像到那一版的 URL（老数据的翻页行为不能坏）。
    expect(useCanvasStore.getState().nodes[0].content).toBe(urlA);
  });

  it('翻到旧版本时，输入框直接显示那一版当时用的提示词', async () => {
    setNode(
      {
        history: [
          { url: urlA, prompt: '一只猫坐在窗台' },
          { url: urlB, prompt: '一只橘猫坐在窗台,逆光' },
        ],
        historyIndex: 1,
        prompt: '一只橘猫坐在窗台,逆光',
      },
      urlB,
    );
    renderNode();

    expect(editor()).toHaveTextContent('一只橘猫坐在窗台,逆光');

    await userEvent.click(screen.getByTitle('上一张'));

    expect(editor()).toHaveTextContent('一只猫坐在窗台');
    expect(useCanvasStore.getState().nodes[0].content).toBe(urlA);

    // 翻回最新版 → 回到草稿(settings.prompt)。
    await userEvent.click(screen.getByTitle('下一张'));
    expect(editor()).toHaveTextContent('一只橘猫坐在窗台,逆光');
  });

  // 停在最新版时必须显示 settings.prompt,而不是最新版已生成的那句——否则用户
  // 改词准备重新生成时,刚打的草稿会被上一版的词盖掉。
  it('停在最新版时显示的是待生成的草稿,不是该版已生成的词', async () => {
    setNode(
      {
        history: [{ url: urlA, prompt: '第一版的词' }, { url: urlB, prompt: '第二版的词' }],
        historyIndex: 1,
        prompt: '我正在写的第三版草稿',
      },
      urlB,
    );
    renderNode();

    expect(editor()).toHaveTextContent('我正在写的第三版草稿');
  });

  // 在旧版上直接改输入框：改动要立刻可见,并且写进 settings.prompt 作为下次生成的词。
  it('在旧版上编辑输入框,内容跟随用户输入并落到 settings.prompt', async () => {
    setNode(
      {
        history: [{ url: urlA, prompt: '旧版的词' }, { url: urlB, prompt: '新版的词' }],
        historyIndex: 0,
        prompt: '新版的词',
      },
      urlA,
    );
    renderNode();

    expect(editor()).toHaveTextContent('旧版的词');

    await userEvent.type(editor(), '改一改');

    expect(editor()).toHaveTextContent('改一改');
    await waitFor(() => {
      expect(String(useCanvasStore.getState().nodes[0].settings!.prompt)).toContain('改一改');
    });
  });

  // 中途关页面 → 后端 sink 独自把结果并进 history（含提示词）→ 用户回来打开画布。
  // 真实形态:关页面前 settings.prompt 就是提交那一版用的词,所以两者一致。
  it('后端在页面关闭期间写好的版本，回来后翻旧版能读到旧版的提示词', async () => {
    setNode(
      {
        history: [
          { url: urlA, prompt: '一只猫坐在窗台' },
          { url: urlB, prompt: '一只橘猫坐在窗台,逆光', at: '2026-07-27T10:00:00Z' },
        ],
        historyIndex: 1,
        prompt: '一只橘猫坐在窗台,逆光',
      },
      urlB,
    );
    renderNode();

    await userEvent.click(screen.getByTitle('上一张'));

    expect(editor()).toHaveTextContent('一只猫坐在窗台');
  });

  it('点生成时把当次提示词/参数落进 pendingGen——结果回来时才有得可记', async () => {
    setNode({ history: [urlA], historyIndex: 0, prompt: '要生成的这句' }, urlA);
    vi.mocked(submitCanvasTask).mockResolvedValue({ taskId: 42 } as never);
    // 提交后节点转入 generating，轮询 effect 会立刻起飞；这里让它永远挂起，
    // 本用例只关心「点下去那一刻有没有把快照落库」。
    vi.mocked(pollCanvasTaskUntilDone).mockReturnValue(new Promise(() => {}) as never);
    renderNode();

    await waitFor(() => expect(screen.getByRole('button', { name: 'Image Model' })).toBeInTheDocument());
    await userEvent.click(screen.getByRole('button', { name: '生成' }));

    await waitFor(() => {
      const s = useCanvasStore.getState().nodes[0].settings!;
      expect(s.pendingGen).toEqual({
        prompt: '要生成的这句',
        params: expect.objectContaining({ ratio: '1:1' }),
        // 提交时刻：计时条刷新后续算的起点
        submittedAt: expect.any(String),
      });
    });
  });
});

describe('GenImageNode 提交按钮禁用', () => {
  // 上游文本节点是提示词的第二个来源（handleGenerate 里 `prompt || fallbackPrompt`），
  // 所以禁用规则必须按「两个来源都空」判定，不能只看输入框。
  function setUp(prompt: string, upstreamText?: string) {
    useCanvasStore.setState({
      nodes: [
        {
          id: 'image-1',
          type: 'gen-image',
          x: 0,
          y: 0,
          width: 280,
          height: 280,
          status: 'idle',
          settings: { ratio: '1:1', n: 1, prompt },
        },
        ...(upstreamText === undefined
          ? []
          : [{
              id: 'text-1',
              type: 'text-node' as const,
              x: 0,
              y: 400,
              width: 200,
              height: 100,
              status: 'idle' as const,
              settings: { text: upstreamText },
            }]),
      ],
      connections:
        upstreamText === undefined ? [] : [{ id: 'c-1', from: 'text-1', to: 'image-1' }],
      selectedIds: new Set(['image-1']),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  }

  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    vi.mocked(listRuntimeModels).mockResolvedValue([imageModel]);
    vi.mocked(loadSeedanceStatus).mockResolvedValue(undefined);
    vi.mocked(api.post).mockResolvedValue({ data: { thumbs: {} } });
  });

  // 模型加载完才有 selectedModelId；否则按钮因「没选模型」而禁用，测不到提示词这条。
  async function renderReady() {
    renderNode();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Image Model' })).toBeInTheDocument(),
    );
  }

  // 按钮的无障碍名固定是 aria-label="生成"（动态原因移到了 AntD Tooltip，不再进 title）
  const submitButton = () => screen.getByRole('button', { name: '生成' });

  it('输入框为空且没有上游文本节点时禁用——没词可提交', async () => {
    setUp('   ');
    await renderReady();

    expect(submitButton()).toBeDisabled();
  });

  it('输入框有词就可提交', async () => {
    setUp('一只猫坐在窗台');
    await renderReady();

    expect(submitButton()).toBeEnabled();
  });

  it('输入框为空但上游连着文本节点时仍可提交——提交的是上游那句', async () => {
    setUp('', '上游文本节点里的提示词');
    await renderReady();

    expect(submitButton()).toBeEnabled();
  });
});

// 故事板详情面板里这张图是主角：占满预览区、用户就是来看清楚的，所以直接上
// 原图，也不需要双击开 Lightbox（开出来还是同一张）。画布上必须相反——节点
// 才两三百像素宽，一张画布可能挂上百个节点，全换原图流量翻好几倍。
describe('GenImageNode 在故事板详情面板里', () => {
  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    vi.mocked(listRuntimeModels).mockResolvedValue([imageModel]);
    vi.mocked(loadSeedanceStatus).mockResolvedValue(undefined);
    vi.mocked(api.post).mockResolvedValue({
      data: { thumbs: { [imageURL]: thumbURL } },
    });
    useCanvasStore.setState({
      nodes: [{
        id: 'image-1',
        type: 'gen-image',
        x: 0,
        y: 0,
        width: 280,
        height: 280,
        status: 'succeeded',
        content: imageURL,
        settings: { history: [imageURL], historyIndex: 0, ratio: '1:1', n: 1 },
      }],
      connections: [],
      selectedIds: new Set(['image-1']),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  });

  it('用原图而不是缩略图', async () => {
    const { container } = renderNodeInStoryboard();
    await waitFor(() => {
      expect(container.querySelector(`img[src="${imageURL}"]`)).not.toBeNull();
    });
    expect(container.querySelector(`img[src="${thumbURL}"]`)).toBeNull();
  });

  it('双击不再开 Lightbox——已经是原图了，开出来还是同一张', async () => {
    const { container } = renderNodeInStoryboard();
    const image = await waitFor(() => {
      const el = container.querySelector(`img[src="${imageURL}"]`);
      expect(el).not.toBeNull();
      return el!;
    });

    fireEvent.doubleClick(image);

    expect(useCanvasStore.getState().lightbox).toBeNull();
    expect(image.className).not.toContain('cursor-zoom-in');
  });

  it('画布模式不受影响：仍用缩略图，双击仍开 Lightbox', async () => {
    const { container } = renderNode();
    const image = await findThumbImage(container);

    expect(image.className).toContain('cursor-zoom-in');
    fireEvent.doubleClick(image);

    expect(useCanvasStore.getState().lightbox).toEqual({
      url: imageURL,
      kind: 'image',
      title: 'AI 绘图',
    });
  });
});

// 故事板参考区的连线编辑：
// 缩略图 hover 出 × 断开、左侧卡片拖进参数面板即连上。
describe('GenImageNode 故事板参考区连线编辑', () => {
  const MIME = 'application/x-tvmao-storyboard-node';

  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    vi.mocked(listRuntimeModels).mockResolvedValue([imageModel]);
    vi.mocked(loadSeedanceStatus).mockResolvedValue(undefined);
    vi.mocked(api.post).mockResolvedValue({ data: { thumbs: {} } });
    useCanvasStore.setState({
      nodes: [
        {
          id: 'image-1', type: 'gen-image', x: 0, y: 0, width: 280, height: 280,
          status: 'idle', settings: { ratio: '1:1', n: 1 },
        },
        {
          id: 'src-1', type: 'input-image', x: 0, y: 0, width: 200, height: 200,
          status: 'succeeded', content: 'https://example.test/ref.png', settings: {},
        },
        {
          id: 'src-2', type: 'input-image', x: 0, y: 0, width: 200, height: 200,
          status: 'succeeded', content: 'https://example.test/ref2.png', settings: {},
        },
      ],
      connections: [{ id: 'e-1', from: 'src-1', to: 'image-1' }],
      selectedIds: new Set(['image-1']),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
      readOnly: false,
    });
  });

  it('缩略图上的 × 断开那条上游连线', async () => {
    renderNodeInStoryboard();
    const close = await screen.findByRole('button', { name: '断开参考图 1' });

    fireEvent.click(close);

    expect(useCanvasStore.getState().connections).toHaveLength(0);
  });

  it('把左侧卡片拖进参数面板：建立连线', () => {
    const { container } = renderNodeInStoryboard();
    const panel = container.querySelector('.rounded-\\[20px\\]') as HTMLElement;
    expect(panel).toBeTruthy();

    fireEvent.drop(panel, {
      dataTransfer: { getData: (t: string) => (t === MIME ? 'src-2' : ''), types: [MIME] },
    });

    const conns = useCanvasStore.getState().connections;
    expect(conns.some((c) => c.from === 'src-2' && c.to === 'image-1')).toBe(true);
  });

  // addConnection 自带去重/自环检查，这条钉死 drop 路径没绕过它。
  it('拖自己进来不建自环，重复拖不重复建线', () => {
    const { container } = renderNodeInStoryboard();
    const panel = container.querySelector('.rounded-\\[20px\\]') as HTMLElement;

    fireEvent.drop(panel, {
      dataTransfer: { getData: (t: string) => (t === MIME ? 'image-1' : ''), types: [MIME] },
    });
    fireEvent.drop(panel, {
      dataTransfer: { getData: (t: string) => (t === MIME ? 'src-1' : ''), types: [MIME] },
    });

    expect(useCanvasStore.getState().connections).toHaveLength(1);
  });

  // 拖拽全程的反馈：拖起来（还没到面板上）就要出灰虚线 + 中央提示，
  // 不是等 dragover 才有反应——用户就是因为"拖着不知道往哪放"提的这条。
  it('拖拽开始后面板出现提示文字，结束后消失', () => {
    renderNodeInStoryboard();
    expect(screen.queryByText('可将图片、视频拖拽到参考区')).toBeNull();

    act(() => beginNodeDrag());
    expect(screen.getByText('可将图片、视频拖拽到参考区')).toBeInTheDocument();

    act(() => endNodeDrag());
    expect(screen.queryByText('可将图片、视频拖拽到参考区')).toBeNull();
  });

  it('画布模式下即使有卡片在拖，也不出现提示（拖拽连线是故事板专属）', () => {
    renderNode();
    act(() => beginNodeDrag());
    expect(screen.queryByText('可将图片、视频拖拽到参考区')).toBeNull();
    act(() => endNodeDrag());
  });

  it('画布模式不受影响：没有 ×，drop 也不建线', async () => {
    const { container } = renderNode();
    await screen.findByTitle('参考图 1');
    expect(screen.queryByRole('button', { name: '断开参考图 1' })).toBeNull();

    const panel = container.querySelector('.rounded-\\[20px\\]') as HTMLElement;
    fireEvent.drop(panel, {
      dataTransfer: { getData: (t: string) => (t === MIME ? 'src-2' : ''), types: [MIME] },
    });
    expect(useCanvasStore.getState().connections).toHaveLength(1);
  });
});

describe('GenImageNode 生成成功清掉上次的未保存标记（I-2）', () => {
  const REMOTE = 'https://upstream.example.com/out/old.png?sig=a';
  const LOCAL = 'http://127.0.0.1:5123/media/gen/new.png?token=t';

  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    vi.mocked(listRuntimeModels).mockResolvedValue([imageModel]);
    vi.mocked(loadSeedanceStatus).mockResolvedValue(undefined);
    vi.mocked(api.post).mockResolvedValue({ data: { thumbs: {} } });
  });

  it('上次下载失败后重新生成成功：unsaved/genTaskId 被清，角标与重试按钮消失', async () => {
    useCanvasStore.setState({
      nodes: [{
        id: 'image-1',
        type: 'gen-image',
        x: 0,
        y: 0,
        width: 280,
        height: 280,
        status: 'generating',
        taskId: 88,
        content: REMOTE,
        settings: { history: [REMOTE], historyIndex: 0, ratio: '1:1', n: 1, unsaved: true, genTaskId: 7 },
      }],
      connections: [],
      selectedIds: new Set<string>(),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
      readOnly: false,
    });
    vi.mocked(pollCanvasTaskUntilDone).mockResolvedValue({
      status: 'succeeded',
      outputUrls: [LOCAL],
      assetIds: [5],
    } as never);
    renderNode();

    await waitFor(() => expect(useCanvasStore.getState().nodes[0].content).toBe(LOCAL));
    const n = useCanvasStore.getState().nodes[0];
    expect(n.status).toBe('succeeded');
    expect(n.settings?.unsaved).toBeUndefined();
    expect(n.settings?.genTaskId).toBeUndefined();
    expect(screen.queryByText('未保存到本地')).toBeNull();
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull();
  });
});
