import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { GenVideoNode } from './GenVideoNode';
import { useCanvasStore } from '../store';
import { listRuntimeModels, uploadAsset, type ModelConfig } from '@/shared/api';
import { loadSeedanceStatus } from '../batchQueries';

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

// 合规状态走合批器（batchQueries.ts）——按节点边界 stub 掉。
vi.mock('../batchQueries', () => ({
  loadSeedanceStatus: vi.fn().mockResolvedValue(undefined),
}));

const videoURL = 'https://example.test/generated.mp4';
const longVideoModelName = 'cm-seedance-2.0-pro-720-15-ny（低价通道）';
const videoModel: ModelConfig = {
  id: 2,
  type: 'video',
  name: 'Video Model',
  modelId: 'video-model',
  basePath: '',
  enabled: true,
  hidden: false,
  sortOrder: 1,
  requestTemplate: {},
  uiSchema: {
    ratio: {
      default: '16:9',
      options: [{ value: '16:9', label: '16:9' }],
    },
    duration: {
      default: 5,
      options: [{ value: 5, label: '5 秒' }],
    },
  },
};

function renderNode() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <GenVideoNode id="video-1" />
    </QueryClientProvider>,
  );
}

describe('GenVideoNode', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(listRuntimeModels).mockResolvedValue([videoModel]);
    vi.mocked(loadSeedanceStatus).mockResolvedValue(undefined);
    useCanvasStore.setState({
      nodes: [{
        id: 'video-1',
        type: 'gen-video',
        x: 0,
        y: 0,
        width: 280,
        height: 180,
        status: 'idle',
        settings: { ratio: '16:9', duration: 5 },
      }],
      connections: [],
      selectedIds: new Set(['video-1']),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  });

  it('选中后不显示估价或积分文案', async () => {
    vi.mocked(listRuntimeModels).mockResolvedValue([{ ...videoModel, priceNote: '480p:70积分/秒|720p:150积分/秒' }]);
    renderNode();

    expect((await screen.findAllByText('Video Model', {}, { timeout: 3000 })).length).toBeGreaterThan(0);
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 700));
    });
    expect(screen.queryByText(/积分|估算中|预计/)).toBeNull();
  });

  it('hides the upload button after a video result exists', () => {
    useCanvasStore.setState({
      nodes: [{
        id: 'video-1',
        type: 'gen-video',
        x: 0,
        y: 0,
        width: 280,
        height: 180,
        status: 'succeeded',
        content: videoURL,
        settings: { ratio: '16:9', duration: 5, history: [videoURL], historyIndex: 0 },
      }],
      selectedIds: new Set(['video-1']),
    });

    renderNode();

    expect(screen.queryByTitle('上传视频（节点将转为视频节点）')).toBeNull();
  });

  it('shows the upload button before a video result exists', () => {
    renderNode();

    expect(screen.getByTitle('上传视频（节点将转为视频节点）')).toBeInTheDocument();
  });

  // 主播放器必须有声、播完即停——muted/loop 曾让用户以为生成的视频没配音。
  it('plays the result video with sound and without looping', () => {
    useCanvasStore.setState({
      nodes: [{
        id: 'video-1',
        type: 'gen-video',
        x: 0,
        y: 0,
        width: 280,
        height: 180,
        status: 'succeeded',
        content: videoURL,
        settings: { ratio: '16:9', duration: 5, history: [videoURL], historyIndex: 0 },
      }],
      selectedIds: new Set(['video-1']),
    });

    const { container } = renderNode();

    const player = container.querySelector('video') as HTMLVideoElement;
    expect(player).not.toBeNull();
    // 原生控制条在 WKWebView 窄节点里会被压坏，统一走自绘 VideoPlayer。
    expect(player).not.toHaveAttribute('controls');
    expect(container.querySelector('[data-video-controls]')).not.toBeNull();
    expect(player.muted).toBe(false);
    expect(player.loop).toBe(false);
  });

  it('shows the resolution option label on the pill, not the raw value', async () => {
    // 后台把分辨率档位的 value 配成子模型 ID（如 cm-seedance-2.0-mini-480-mg），
    // pill 上必须展示 label（480p），否则用户看到的是内部 ID。
    vi.mocked(listRuntimeModels).mockResolvedValue([
      {
        ...videoModel,
        uiSchema: {
          ...videoModel.uiSchema,
          resolution: {
            default: 'cm-seedance-2.0-mini-480-mg',
            options: [
              { value: 'cm-seedance-2.0-mini-480-mg', label: '480p' },
              { value: 'cm-seedance-2.0-mini-720-mg', label: '720p' },
            ],
          },
        },
      },
    ]);
    renderNode();

    expect(await screen.findByText('16:9 · 480p')).toBeInTheDocument();
    expect(screen.queryByText(/cm-seedance-2\.0-mini-480-mg/)).toBeNull();
  });

  it('shows a wide responsive model menu without truncating long names', async () => {
    vi.mocked(listRuntimeModels).mockResolvedValue([
      { ...videoModel, name: longVideoModelName },
    ]);
    renderNode();

    const trigger = await screen.findByRole('button', { name: longVideoModelName });
    await userEvent.click(trigger);

    const labels = screen.getAllByText(longVideoModelName);
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
});

// 与 GenImageNode 同源的保证：老快照（history 是纯 URL 数组）必须零变化，
// 翻到新条目的旧版本时输入框才换成那一版的词。视频节点的提示词字段是
// settings.videoPrompt。
describe('GenVideoNode 版本提示词', () => {
  const urlA = 'https://example.test/v1.mp4';
  const urlB = 'https://example.test/v2.mp4';

  function setNode(settings: Record<string, unknown>, content: string) {
    useCanvasStore.setState({
      nodes: [{
        id: 'video-1',
        type: 'gen-video',
        x: 0,
        y: 0,
        width: 320,
        height: 220,
        status: 'succeeded',
        content,
        settings: { ratio: '16:9', duration: 5, ...settings },
      }],
      connections: [],
      selectedIds: new Set(['video-1']),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  }

  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    vi.mocked(listRuntimeModels).mockResolvedValue([videoModel]);
    vi.mocked(loadSeedanceStatus).mockResolvedValue(undefined);
  });

  const editor = () => screen.getByRole('textbox', { name: '生成提示词' });

  it('老快照(history 是纯 URL 数组)翻页时输入框内容不变——存量画布零变化', async () => {
    setNode({ history: [urlA, urlB], historyIndex: 1, videoPrompt: '最新那句' }, urlB);
    renderNode();

    expect(editor()).toHaveTextContent('最新那句');

    await userEvent.click(screen.getByTitle('上一个'));
    expect(useCanvasStore.getState().nodes[0].settings!.historyIndex).toBe(0);
    expect(editor()).toHaveTextContent('最新那句');
    expect(useCanvasStore.getState().nodes[0].content).toBe(urlA);
  });

  it('翻到旧版本时，输入框直接显示那一版当时用的提示词', async () => {
    setNode(
      {
        history: [
          { url: urlA, prompt: '镜头缓慢推进' },
          { url: urlB, prompt: '镜头缓慢推进,逆光' },
        ],
        historyIndex: 1,
        videoPrompt: '镜头缓慢推进,逆光',
      },
      urlB,
    );
    renderNode();

    expect(editor()).toHaveTextContent('镜头缓慢推进,逆光');

    await userEvent.click(screen.getByTitle('上一个'));

    expect(editor()).toHaveTextContent('镜头缓慢推进');
    expect(useCanvasStore.getState().nodes[0].content).toBe(urlA);
  });

  // 停在最新版时显示待生成的草稿,不是该版已生成的词——否则用户改词准备重新
  // 生成时,刚打的草稿会被上一版的词盖掉。
  it('停在最新版时显示的是待生成的草稿', async () => {
    setNode(
      {
        history: [{ url: urlA, prompt: '第一版' }, { url: urlB, prompt: '第二版' }],
        historyIndex: 1,
        videoPrompt: '我正在写的第三版草稿',
      },
      urlB,
    );
    renderNode();

    expect(editor()).toHaveTextContent('我正在写的第三版草稿');
  });
});

describe('GenVideoNode 提交按钮禁用', () => {
  // 上游文本节点是提示词的第二个来源（handleGenerate 里 `videoPrompt || fallbackPrompt`），
  // 所以禁用规则必须按「两个来源都空」判定，不能只看输入框。
  function setUp(videoPrompt: string, upstreamText?: string) {
    useCanvasStore.setState({
      nodes: [
        {
          id: 'video-1',
          type: 'gen-video',
          x: 0,
          y: 0,
          width: 280,
          height: 180,
          status: 'idle',
          settings: { ratio: '16:9', duration: 5, videoPrompt },
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
        upstreamText === undefined ? [] : [{ id: 'c-1', from: 'text-1', to: 'video-1' }],
      selectedIds: new Set(['video-1']),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  }

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(listRuntimeModels).mockResolvedValue([videoModel]);
    vi.mocked(loadSeedanceStatus).mockResolvedValue(undefined);
  });

  // 模型加载完才有 selectedModelId；否则按钮因「没选模型」而禁用，测不到提示词这条。
  async function renderReady() {
    renderNode();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Video Model' })).toBeInTheDocument(),
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
    setUp('一只猫跳上窗台');
    await renderReady();

    expect(submitButton()).toBeEnabled();
  });

  it('输入框为空但上游连着文本节点时仍可提交——提交的是上游那句', async () => {
    setUp('', '上游文本节点里的提示词');
    await renderReady();

    expect(submitButton()).toBeEnabled();
  });
});

// 高级编辑入口只属于视频编辑类模型（显示条件字面匹配
// selectedModelId.startsWith('doubao-seedance') && includes('edit')）——将来模型
// 多了再收进 gen_control 配置，本次先按用户指定的字符串条件接线。
describe('GenVideoNode 高级编辑入口', () => {
  const editModel: ModelConfig = {
    ...videoModel,
    modelId: 'doubao-seedance-2-5-edit',
    name: 'Seedance 2.5 Edit',
  };

  function setUp(model: ModelConfig, { connectVideo = false } = {}) {
    vi.mocked(listRuntimeModels).mockResolvedValue([model]);
    useCanvasStore.setState({
      nodes: [
        {
          id: 'video-1',
          type: 'gen-video',
          x: 0,
          y: 0,
          width: 280,
          height: 180,
          status: 'idle',
          settings: { ratio: '16:9', duration: 5, modelId: model.modelId },
        },
        ...(connectVideo
          ? [{
              id: 'src-1',
              type: 'preview' as const,
              x: 0,
              y: -400,
              width: 200,
              height: 100,
              status: 'succeeded' as const,
              content: 'https://example.test/upstream.mp4',
              settings: { kind: 'video' },
            }]
          : []),
      ],
      connections: connectVideo ? [{ id: 'c-1', from: 'src-1', to: 'video-1' }] : [],
      selectedIds: new Set(['video-1']),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  }

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(loadSeedanceStatus).mockResolvedValue(undefined);
  });

  it('视频编辑模型显示「高级编辑」按钮', async () => {
    setUp(editModel);
    renderNode();

    expect(await screen.findByRole('button', { name: '高级编辑' })).toBeInTheDocument();
  });

  it('普通模型不显示「高级编辑」按钮', async () => {
    setUp(videoModel);
    renderNode();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Video Model' })).toBeInTheDocument(),
    );

    expect(screen.queryByRole('button', { name: '高级编辑' })).toBeNull();
  });

  it('未连接视频输入时「高级编辑」按钮禁用', async () => {
    setUp(editModel);
    renderNode();

    expect(await screen.findByRole('button', { name: '高级编辑' })).toBeDisabled();
  });

  it('已连接视频输入时「高级编辑」按钮可用', async () => {
    setUp(editModel, { connectVideo: true });
    renderNode();

    expect(await screen.findByRole('button', { name: '高级编辑' })).toBeEnabled();
  });

  // C1 回归：弹框必须 portal 到 document.body，不能嵌在 GenVideoNode 自己的
  // DOM 子树里——节点本身处于 CanvasEditor 的 transform:translate/scale 容器
  // 内，那层 transform 会成为内部 fixed 后代的包含块，画布缩放（zoom≠1）时
  // 弹框的 `fixed inset-0` 就不再等于视口，遮罩和面板整体错位。
  it('高级编辑弹框 portal 到 document.body，不嵌在节点自身的 DOM 子树内', async () => {
    setUp(editModel, { connectVideo: true });
    const { container } = renderNode();

    await userEvent.click(await screen.findByRole('button', { name: '高级编辑' }));

    expect(await screen.findByRole('button', { name: '关闭' })).toBeInTheDocument();
    expect(container.querySelector('[aria-label="关闭"]')).toBeNull();
  });

  // I5 回归：保存链路必须走 uploadFileIntoNode 这一个上传入口（先建 input-image
  // 空节点、再把文件上传进它），而不是自己拼 uploadAsset+updateNode 绕开它；
  // I3 回归：全部成功后由弹框自己调 onClose 关框，不是 GenVideoNode 手动
  // setAdvEditOpen(false)。一次操作把 materialize→connect→appendPrompt→关框
  // 整条链路串起来验证。
  it('保存高级编辑标注：经 uploadFileIntoNode 建图片节点、连线、追加提示词、全部成功后关框', async () => {
    vi.mocked(uploadAsset).mockResolvedValue({
      id: 1, url: 'https://example.test/frame.png', size: 10, mimeType: 'image/png',
    });
    const toBlobSpy = vi
      .spyOn(HTMLCanvasElement.prototype, 'toBlob')
      .mockImplementation(function toBlobStub(this: HTMLCanvasElement, cb: BlobCallback) {
        cb(new Blob(['x']));
      });
    try {
      setUp(editModel, { connectVideo: true });
      renderNode();

      await userEvent.click(await screen.findByRole('button', { name: '高级编辑' }));
      await userEvent.click(screen.getByRole('button', { name: '定位点' }));
      const overlay = document.querySelector('canvas')!;
      fireEvent.pointerDown(overlay, { clientX: 10, clientY: 10 });
      // 确认交互引入后，落笔先进待确认缓冲，「添加至输入框」才生成记录。
      await userEvent.click(screen.getByRole('button', { name: '添加至输入框' }));

      await userEvent.type(
        screen.getByPlaceholderText('说明这处标注（可选，进提示词）'),
        '项链变成黄金项链',
      );
      await userEvent.click(screen.getByRole('button', { name: '保存' }));

      await waitFor(() => expect(uploadAsset).toHaveBeenCalled());
      // 全部成功——弹框自己调 onClose，关闭按钮从文档里消失。
      await waitFor(() => expect(screen.queryByRole('button', { name: '关闭' })).toBeNull());

      const nodes = useCanvasStore.getState().nodes;
      const imageNode = nodes.find((n) => n.type === 'input-image');
      expect(imageNode?.content).toBe('https://example.test/frame.png');
      // uploadFileIntoNode 落库的字段：assetId/mimeType，证明走的是它而不是
      // 自己拼一套 upload+createImageNode。
      expect(imageNode?.settings?.assetId).toBe(1);
      expect(imageNode?.settings?.mimeType).toBe('image/png');

      const connections = useCanvasStore.getState().connections;
      expect(connections.some((c) => c.from === imageNode?.id && c.to === 'video-1')).toBe(true);

      const genNode = nodes.find((n) => n.id === 'video-1');
      expect(String(genNode?.settings?.videoPrompt)).toContain(`@[图片:${imageNode?.id}] 00:00 视频帧标注 项链变成黄金项链`);
    } finally {
      toBlobSpy.mockRestore();
    }
  });

  // I5 复审修复回归：materialize 是「先建 input-image 空节点、再 uploadFileIntoNode
  // 上传」——上传失败时空节点如果留着，加上弹框把失败条目留在记录条等用户重试
  // （entries.slice(res.ok)），每重试一次就会再建一个新节点、留一个孤儿。
  // materialize 必须在抛错前把刚建的节点用 deleteNodes 删掉，重试才是从干净
  // 状态开始，不是越攒越多的空节点。
  it('保存时上传失败：materialize 建的空节点连带被删，不留孤儿', async () => {
    vi.mocked(uploadAsset).mockRejectedValue(new Error('上传失败：网络错误'));
    const toBlobSpy = vi
      .spyOn(HTMLCanvasElement.prototype, 'toBlob')
      .mockImplementation(function toBlobStub(this: HTMLCanvasElement, cb: BlobCallback) {
        cb(new Blob(['x']));
      });
    try {
      setUp(editModel, { connectVideo: true });
      renderNode();

      await userEvent.click(await screen.findByRole('button', { name: '高级编辑' }));
      await userEvent.click(screen.getByRole('button', { name: '定位点' }));
      const overlay = document.querySelector('canvas')!;
      fireEvent.pointerDown(overlay, { clientX: 10, clientY: 10 });
      // 确认交互引入后，落笔先进待确认缓冲，「添加至输入框」才生成记录。
      await userEvent.click(screen.getByRole('button', { name: '添加至输入框' }));

      await userEvent.type(
        screen.getByPlaceholderText('说明这处标注（可选，进提示词）'),
        '备注',
      );
      await userEvent.click(screen.getByRole('button', { name: '保存' }));

      await waitFor(() => expect(uploadAsset).toHaveBeenCalled());
      // 失败文案出现，证明 materialize 确实抛错、走到了 saveAnnotations 的
      // catch 分支（而不是静默吞掉）。
      await screen.findByText(/上传失败：网络错误/);

      // 核心断言：uploadFileIntoNode 内部会先把节点标成 status:'failed' 再
      // 返回 null，materialize 读到这个失败节点后必须把它整个删掉——画布上
      // 不应该残留任何 input-image 节点。
      const nodes = useCanvasStore.getState().nodes;
      expect(nodes.some((n) => n.type === 'input-image')).toBe(false);
    } finally {
      toBlobSpy.mockRestore();
    }
  });

  // 同一次保存的多条标注拼同一行、中文逗号分隔：
  // 每条独占一行会把输入框撑得很长。批次边界 = 一次 onSave 闭包。
  it('保存多条标注：同一行中文逗号分隔，不是一条一行', async () => {
    vi.mocked(uploadAsset).mockResolvedValue({
      id: 1, url: 'https://example.test/frame.png', size: 10, mimeType: 'image/png',
    });
    const toBlobSpy = vi
      .spyOn(HTMLCanvasElement.prototype, 'toBlob')
      .mockImplementation(function toBlobStub(this: HTMLCanvasElement, cb: BlobCallback) {
        cb(new Blob(['x']));
      });
    try {
      setUp(editModel, { connectVideo: true });
      renderNode();

      await userEvent.click(await screen.findByRole('button', { name: '高级编辑' }));
      await userEvent.click(screen.getByRole('button', { name: '定位点' }));
      const overlay = document.querySelector('canvas')!;
      // 两轮「落笔 → 添加至输入框」= 两条独立记录
      fireEvent.pointerDown(overlay, { clientX: 10, clientY: 10 });
      await userEvent.click(screen.getByRole('button', { name: '添加至输入框' }));
      // 确认即退出标注态（commitPending 里 setTool(null)），第二轮要重新选工具
      await userEvent.click(screen.getByRole('button', { name: '定位点' }));
      fireEvent.pointerDown(overlay, { clientX: 30, clientY: 30 });
      await userEvent.click(screen.getByRole('button', { name: '添加至输入框' }));

      await userEvent.click(screen.getByRole('button', { name: '保存' }));
      await waitFor(() => expect(screen.queryByRole('button', { name: '关闭' })).toBeNull());

      const prompt = String(
        useCanvasStore.getState().nodes.find((n) => n.id === 'video-1')?.settings?.videoPrompt,
      );
      expect(prompt).not.toContain('\n');
      expect(prompt).toMatch(/^@\[图片:[^\]]+\] 00:00 视频帧标注，@\[图片:[^\]]+\] 00:00 视频帧标注$/);
    } finally {
      toBlobSpy.mockRestore();
    }
  });
});

// 命名引用对账：提示词里的 `@张三` 与连线里同名图片节点对上就变 chip 标记，断线
// 还原成 `@张三`，重连再变回来。逻辑在 linkNamedMentions/stripDeadMentions 已单测，
// 这里钉的是节点 effect 的接线：连线变化与提示词变化都要触发对账。
describe('GenVideoNode 命名引用对账', () => {
  const imageNode = {
    id: 'img-zs',
    type: 'input-image' as const,
    x: 0,
    y: 0,
    width: 200,
    height: 200,
    content: 'https://x/zs.png',
    settings: { name: '张三' },
  };
  const link = { id: 'c1', from: 'img-zs', to: 'video-1' };
  const promptOf = () =>
    useCanvasStore.getState().nodes.find((n) => n.id === 'video-1')!.settings!.videoPrompt as string;

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(listRuntimeModels).mockResolvedValue([videoModel]);
    vi.mocked(loadSeedanceStatus).mockResolvedValue(undefined);
    useCanvasStore.setState({
      nodes: [
        {
          id: 'video-1',
          type: 'gen-video',
          x: 0,
          y: 0,
          width: 280,
          height: 180,
          status: 'idle',
          settings: { ratio: '16:9', duration: 5, videoPrompt: '@张三走进房间' },
        },
        imageNode,
      ],
      connections: [],
      selectedIds: new Set(['video-1']),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  });

  it('连线 → 变 chip；断线 → 还原原文；重连 → 再变 chip', async () => {
    renderNode();
    // 未连线：名字对不上，原样不动。
    expect(promptOf()).toBe('@张三走进房间');

    act(() => {
      useCanvasStore.setState({ connections: [link] });
    });
    await waitFor(() => expect(promptOf()).toBe('@[图片:img-zs|张三]走进房间'));

    act(() => {
      useCanvasStore.setState({ connections: [] });
    });
    await waitFor(() => expect(promptOf()).toBe('@张三走进房间'));

    act(() => {
      useCanvasStore.setState({ connections: [link] });
    });
    await waitFor(() => expect(promptOf()).toBe('@[图片:img-zs|张三]走进房间'));
  });

  it('用户在输入框打过字之后（promptDraft 接管显示）连线，chip 也必须显示出来', async () => {
    // 回归：对账 effect 只写 settings.videoPrompt 时，编辑器显示的是 promptDraft，
    // store 里已是 chip 标记而界面永远停在原文；再敲一个字又把原文写回 store，死循环。
    useCanvasStore.setState((s) => ({
      nodes: s.nodes.map((n) => (n.id === 'video-1' ? { ...n, settings: { ...n.settings, videoPrompt: '' } } : n)),
    }));
    renderNode();
    const box = screen.getByRole('textbox', { name: '生成提示词' });
    await userEvent.type(box, '@张三走进房间');
    await waitFor(() => expect(promptOf()).toBe('@张三走进房间'));

    act(() => {
      useCanvasStore.setState({ connections: [link] });
    });
    await waitFor(() => expect(promptOf()).toBe('@[图片:img-zs|张三]走进房间'));
    // 界面：chip 已渲染，原文不再以纯文本出现。
    await waitFor(() => expect(box.querySelector('[data-mention]')).not.toBeNull());
    expect(box.querySelector('[data-mention]')!.textContent).toContain('图片 1');
    expect(box.textContent).not.toContain('@张三');
  });

  it('已连线时编辑提示词写出 @[角色-张三] 也会就地变 chip', async () => {
    useCanvasStore.setState({ connections: [link] });
    renderNode();
    await waitFor(() => expect(promptOf()).toBe('@[图片:img-zs|张三]走进房间'));

    act(() => {
      useCanvasStore.getState().updateNodeSettings('video-1', { videoPrompt: '@[角色-张三]转身' });
    });
    await waitFor(() => expect(promptOf()).toBe('@[图片:img-zs|[角色-张三]]转身'));
  });
});
