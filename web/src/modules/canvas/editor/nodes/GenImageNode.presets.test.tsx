// AI 绘图节点「提示词预设」的用户侧交互。
// 核心约定在这里全部钉死：整套功能绑模型（命中 config.models 才有入口）、单选
// chip（重选替换/删除置空/切模型自动清）、图片数约束禁用生成、提交只带 presetKey
// （prompt 正文对前端保密，前端不做任何拼接）。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { GenImageNode } from './GenImageNode';
import { useCanvasStore } from '../store';
import {
  api,
  fetchGenImagePresets,
  listRuntimeModels,
  pollCanvasTaskUntilDone,
  submitCanvasTask,
  type GenImagePresetsConfig,
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
    fetchGenImagePresets: vi.fn().mockResolvedValue({ models: [], presets: [] }),
    submitCanvasTask: vi.fn(),
    pollCanvasTaskUntilDone: vi.fn(),
    uploadAsset: vi.fn(),
  };
});

vi.mock('../batchQueries', () => ({
  loadSeedanceStatus: vi.fn().mockResolvedValue(undefined),
}));

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
    ratio: { default: '1:1', options: [{ value: '1:1', label: '1:1' }] },
    n: { default: 1, options: [{ value: 1, label: '1' }] },
  },
};

// 第二个模型**不在**预设配置的 models 里——「切到未绑定模型自动清 chip」用它。
const unboundModel: ModelConfig = {
  ...imageModel,
  id: 2,
  name: 'Unbound Model',
  modelId: 'unbound-model',
};

const presetsConfig: GenImagePresetsConfig = {
  models: ['image-model'],
  presets: [
    {
      key: 'three_view',
      value: {
        name: '角色三视图',
        category: '设定图',
        description: '正侧背视图与脸部特写',
        placeholder: '点击生成，直接基于当前图像生成白底三视图。',
        minImages: 1,
        maxImages: 1,
        sort: 10,
      },
    },
    {
      key: 'emotion_board',
      value: { name: '情绪版', category: '设定图', sort: 20 },
    },
    // 带 params 的预设：⚙ 打开参数面板而不是换预设菜单。fusion 标了 default
    // 在中间档，skin 故意不标——「没 default 就高亮第一个」这条兜底要测到。
    {
      key: 'portrait_tune',
      value: {
        name: '人像质感调节',
        category: '画质',
        sort: 30,
        params: [
          {
            key: 'fusion',
            name: '人景融合',
            options: [
              { key: 'weak', name: '弱' },
              { key: 'std', name: '标准', default: true },
              { key: 'strong', name: '强' },
            ],
          },
          {
            key: 'skin',
            name: '皮肤',
            options: [
              { key: 'smooth', name: '柔滑' },
              { key: 'real', name: '真实' },
              { key: 'texture', name: '质感' },
            ],
          },
        ],
      },
    },
  ],
};

function setNode(settings: Record<string, unknown>, withUpstreamImage = false) {
  useCanvasStore.setState({
    nodes: [
      {
        id: 'image-1',
        type: 'gen-image',
        x: 0,
        y: 0,
        width: 280,
        height: 280,
        status: 'idle' as const,
        settings: { modelId: 'image-model', ratio: '1:1', n: 1, ...settings },
      },
      ...(withUpstreamImage
        ? [{
            id: 'src-1',
            type: 'input-image' as const,
            x: 0,
            y: 400,
            width: 200,
            height: 200,
            status: 'succeeded' as const,
            content: 'https://example.test/ref.png',
            settings: {},
          }]
        : []),
    ],
    connections: withUpstreamImage ? [{ id: 'c-1', from: 'src-1', to: 'image-1' }] : [],
    selectedIds: new Set(['image-1']),
    view: { x: 0, y: 0, zoom: 1 },
    lightbox: null,
    readOnly: false,
  });
}

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

const presetButton = () => screen.queryByRole('button', { name: '预设' });
const settingsOf = () =>
  useCanvasStore.getState().nodes.find((n) => n.id === 'image-1')!.settings!;

// 模型加载完才有稳定的 selectedModelId/预设入口，先等模型 pill 出现。
async function renderReady(modelName = 'Image Model') {
  renderNode();
  await waitFor(() =>
    expect(screen.getByRole('button', { name: modelName })).toBeInTheDocument(),
  );
}

describe('GenImageNode 提示词预设', () => {
  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    vi.mocked(listRuntimeModels).mockResolvedValue([imageModel, unboundModel]);
    vi.mocked(fetchGenImagePresets).mockResolvedValue(presetsConfig);
    vi.mocked(loadSeedanceStatus).mockResolvedValue(undefined);
    vi.mocked(api.post).mockResolvedValue({ data: { thumbs: {} } });
  });

  it('当前模型命中配置的 models 才显示菜单按钮', async () => {
    setNode({});
    await renderReady();

    await waitFor(() => expect(presetButton()).toBeInTheDocument());
  });

  it('当前模型不在 models 里时完全不渲染菜单按钮', async () => {
    setNode({ modelId: 'unbound-model' });
    await renderReady('Unbound Model');

    // 等预设配置这条 query 也 settle，再断言按钮自始至终没出现。
    await waitFor(() => expect(fetchGenImagePresets).toHaveBeenCalled());
    expect(presetButton()).toBeNull();
  });

  it('选中预设：写入 settings.promptPreset 并渲染 chip', async () => {
    setNode({});
    await renderReady();
    await waitFor(() => expect(presetButton()).toBeInTheDocument());

    await userEvent.click(presetButton()!);
    // 菜单按 category 分组，条目带 description 副标题。
    expect(screen.getByText('设定图')).toBeInTheDocument();
    expect(screen.getByText('正侧背视图与脸部特写')).toBeInTheDocument();
    await userEvent.click(screen.getByText('角色三视图'));

    expect(settingsOf().promptPreset).toBe('three_view');
    // 菜单已关、chip 出现（chip 上的名字 + 删除钮）。
    expect(screen.queryByText('情绪版')).toBeNull();
    expect(screen.getByText('角色三视图')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '移除预设' })).toBeInTheDocument();
  });

  it('重选另一个预设即替换（单选）', async () => {
    setNode({ promptPreset: 'three_view' });
    await renderReady();
    await waitFor(() => expect(presetButton()).toBeInTheDocument());

    await userEvent.click(presetButton()!);
    await userEvent.click(screen.getByText('情绪版'));

    expect(settingsOf().promptPreset).toBe('emotion_board');
    expect(screen.getByText('情绪版')).toBeInTheDocument();
    expect(screen.queryByText('角色三视图')).toBeNull();
  });

  it('chip 上的 × 删除置空', async () => {
    setNode({ promptPreset: 'emotion_board' });
    await renderReady();
    await waitFor(() => expect(screen.getByText('情绪版')).toBeInTheDocument());

    await userEvent.click(screen.getByRole('button', { name: '移除预设' }));

    expect(settingsOf().promptPreset).toBeUndefined();
    expect(screen.queryByText('情绪版')).toBeNull();
  });

  it('切到未绑定预设的模型：chip 消失且 promptPreset 自动清空', async () => {
    setNode({ promptPreset: 'emotion_board' });
    await renderReady();
    await waitFor(() => expect(screen.getByText('情绪版')).toBeInTheDocument());

    // 通过模型菜单真实切换到未绑定模型（不是直接改 store）。
    await userEvent.click(screen.getByRole('button', { name: 'Image Model' }));
    await userEvent.click(screen.getByText('Unbound Model'));

    await waitFor(() => expect(settingsOf().promptPreset).toBeUndefined());
    expect(screen.queryByText('情绪版')).toBeNull();
    expect(presetButton()).toBeNull();
  });

  it('配置里查不到已存的 key（被后台删了）：chip 不渲染，视为未选', async () => {
    setNode({ promptPreset: 'deleted_key', prompt: '一只猫' });
    await renderReady();
    await waitFor(() => expect(presetButton()).toBeInTheDocument());

    expect(screen.queryByRole('button', { name: '移除预设' })).toBeNull();
  });

  it('图片数不满足约束：生成按钮禁用并显示灰字提示', async () => {
    // three_view 要求恰好 1 张输入图；不连图 + 有提示词 → 只因图片数被禁。
    setNode({ promptPreset: 'three_view', prompt: '一只猫' });
    await renderReady();
    await waitFor(() => expect(presetButton()).toBeInTheDocument());

    expect(screen.getByRole('button', { name: '生成' })).toBeDisabled();
    expect(screen.getByText('该预设需要恰好 1 张图片')).toBeInTheDocument();
  });

  it('超过模型 inputLimits.image.max：生成按钮禁用并显示与后端一致的原因', async () => {
    vi.mocked(listRuntimeModels).mockResolvedValue([
      { ...imageModel, genControl: { inputLimits: { image: { max: 0 } } } },
      unboundModel,
    ]);
    setNode({ prompt: '一只猫' }, true);
    await renderReady();

    await waitFor(() => expect(screen.getByRole('button', { name: '生成' })).toBeDisabled());
    expect(screen.getByText('该模型不支持连接图片')).toBeInTheDocument();
  });

  it('未超过模型 inputLimits.image.max：生成按钮可用', async () => {
    vi.mocked(listRuntimeModels).mockResolvedValue([
      { ...imageModel, genControl: { inputLimits: { image: { max: 5 } } } },
      unboundModel,
    ]);
    setNode({ prompt: '一只猫' }, true);
    await renderReady();

    expect(screen.getByRole('button', { name: '生成' })).toBeEnabled();
    expect(screen.queryByText(/该模型最多支持连接/)).toBeNull();
  });

  it('图片数满足约束后生成按钮恢复可用', async () => {
    setNode({ promptPreset: 'three_view', prompt: '一只猫' }, true);
    await renderReady();
    await waitFor(() => expect(presetButton()).toBeInTheDocument());

    expect(screen.getByRole('button', { name: '生成' })).toBeEnabled();
    expect(screen.queryByText('该预设需要恰好 1 张图片')).toBeNull();
  });

  it('选中预设且输入框为空：placeholder 换成预设文案，且可直接生成', async () => {
    // 预设正文在服务端拼接，用户可以一个字不写直接点生成——placeholder 的
    // 「点击生成…」引导必须是真的（按钮不能因空提示词而禁用）。
    setNode({ promptPreset: 'three_view' }, true);
    await renderReady();
    await waitFor(() => expect(presetButton()).toBeInTheDocument());

    expect(
      screen.getByText('点击生成，直接基于当前图像生成白底三视图。'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '生成' })).toBeEnabled();
  });

  it('提交时 submitCanvasTask 带 presetKey，pendingGen 落 presetKey+presetName 快照', async () => {
    setNode({ promptPreset: 'three_view', prompt: '一只猫' }, true);
    vi.mocked(submitCanvasTask).mockResolvedValue({ taskId: 42 } as never);
    vi.mocked(pollCanvasTaskUntilDone).mockReturnValue(new Promise(() => {}) as never);
    await renderReady();
    await waitFor(() => expect(presetButton()).toBeInTheDocument());

    await userEvent.click(screen.getByRole('button', { name: '生成' }));

    await waitFor(() =>
      expect(submitCanvasTask).toHaveBeenCalledWith(
        expect.objectContaining({ presetKey: 'three_view' }),
      ),
    );
    // 前端不做任何 prompt 拼接：inputs.prompt 仍是纯用户词。
    const input = vi.mocked(submitCanvasTask).mock.calls[0][0];
    expect(input.inputs.prompt).toBe('一只猫');
    // history 条目落库前的快照带 key + 当时的 name（展示用 name，key 不进视觉层）。
    expect(settingsOf().pendingGen).toMatchObject({
      presetKey: 'three_view',
      presetName: '角色三视图',
    });
  });

  it('promptPreset 为空时提交体不带 presetKey', async () => {
    setNode({ prompt: '一只猫' });
    vi.mocked(submitCanvasTask).mockResolvedValue({ taskId: 42 } as never);
    vi.mocked(pollCanvasTaskUntilDone).mockReturnValue(new Promise(() => {}) as never);
    await renderReady();
    await waitFor(() => expect(presetButton()).toBeInTheDocument());

    await userEvent.click(screen.getByRole('button', { name: '生成' }));

    await waitFor(() => expect(submitCanvasTask).toHaveBeenCalled());
    expect(vi.mocked(submitCanvasTask).mock.calls[0][0]).not.toHaveProperty('presetKey');
  });

  // ── 预设参数面板（带 params 的预设，chip ⚙ 的分叉行为） ──────────────────
  const paramsGear = () => screen.getByRole('button', { name: '预设参数' });
  const optionBtn = (name: string) => screen.getByRole('button', { name });

  it('带 params 的预设：⚙ 打开参数面板而不是换预设菜单', async () => {
    setNode({ promptPreset: 'portrait_tune' });
    await renderReady();
    await waitFor(() => expect(screen.getByText('人像质感调节')).toBeInTheDocument());

    await userEvent.click(paramsGear());

    // 面板 = 维度分组标题 + 档位按钮；换预设菜单的条目（其它预设名/分组）不出现。
    expect(screen.getByText('人景融合')).toBeInTheDocument();
    expect(screen.getByText('皮肤')).toBeInTheDocument();
    expect(optionBtn('标准')).toBeInTheDocument();
    expect(screen.queryByText('情绪版')).toBeNull();
    expect(screen.queryByText('角色三视图')).toBeNull();
  });

  it('无 params 的预设：⚙ 维持原样打开换预设菜单', async () => {
    setNode({ promptPreset: 'emotion_board' });
    await renderReady();
    await waitFor(() => expect(screen.getByText('情绪版')).toBeInTheDocument());

    await userEvent.click(screen.getByRole('button', { name: '更换预设' }));

    // 出的是换预设菜单（其它预设可点），不是参数面板。
    expect(screen.getByText('角色三视图')).toBeInTheDocument();
    expect(screen.queryByText('人景融合')).toBeNull();
  });

  it('默认高亮：选中态落在 default 档，没标 default 的维度落第一个', async () => {
    setNode({ promptPreset: 'portrait_tune' });
    await renderReady();
    await waitFor(() => expect(screen.getByText('人像质感调节')).toBeInTheDocument());

    await userEvent.click(paramsGear());

    // fusion 的 default 是「标准」；skin 没标 default → 第一个「柔滑」。
    expect(optionBtn('标准')).toHaveAttribute('aria-pressed', 'true');
    expect(optionBtn('弱')).toHaveAttribute('aria-pressed', 'false');
    expect(optionBtn('柔滑')).toHaveAttribute('aria-pressed', 'true');
    expect(optionBtn('真实')).toHaveAttribute('aria-pressed', 'false');
  });

  it('点档位：写入 settings.presetParams、高亮切换、面板不关闭', async () => {
    setNode({ promptPreset: 'portrait_tune' });
    await renderReady();
    await waitFor(() => expect(screen.getByText('人像质感调节')).toBeInTheDocument());

    await userEvent.click(paramsGear());
    await userEvent.click(optionBtn('强'));

    // 只存用户选过的维度，不补全其它维度（服务端对缺失维度落 default）。
    expect(settingsOf().presetParams).toEqual({ fusion: 'strong' });
    expect(optionBtn('强')).toHaveAttribute('aria-pressed', 'true');
    expect(optionBtn('标准')).toHaveAttribute('aria-pressed', 'false');
    // 面板还开着（用户常连调多个维度）——接着调第二个维度。
    await userEvent.click(optionBtn('质感'));
    expect(settingsOf().presetParams).toEqual({ fusion: 'strong', skin: 'texture' });
  });

  it('提交时 submitCanvasTask 带 presetParams，pendingGen 落同一份快照', async () => {
    setNode({
      promptPreset: 'portrait_tune',
      presetParams: { fusion: 'strong' },
      prompt: '一只猫',
    });
    vi.mocked(submitCanvasTask).mockResolvedValue({ taskId: 42 } as never);
    vi.mocked(pollCanvasTaskUntilDone).mockReturnValue(new Promise(() => {}) as never);
    await renderReady();
    await waitFor(() => expect(screen.getByText('人像质感调节')).toBeInTheDocument());

    await userEvent.click(screen.getByRole('button', { name: '生成' }));

    await waitFor(() =>
      expect(submitCanvasTask).toHaveBeenCalledWith(
        expect.objectContaining({
          presetKey: 'portrait_tune',
          presetParams: { fusion: 'strong' },
        }),
      ),
    );
    expect(settingsOf().pendingGen).toMatchObject({
      presetKey: 'portrait_tune',
      presetParams: { fusion: 'strong' },
    });
  });

  it('无 params 的预设提交体不带 presetParams', async () => {
    setNode({ promptPreset: 'three_view', prompt: '一只猫' }, true);
    vi.mocked(submitCanvasTask).mockResolvedValue({ taskId: 42 } as never);
    vi.mocked(pollCanvasTaskUntilDone).mockReturnValue(new Promise(() => {}) as never);
    await renderReady();
    await waitFor(() => expect(presetButton()).toBeInTheDocument());

    await userEvent.click(screen.getByRole('button', { name: '生成' }));

    await waitFor(() => expect(submitCanvasTask).toHaveBeenCalled());
    expect(vi.mocked(submitCanvasTask).mock.calls[0][0]).not.toHaveProperty('presetParams');
  });

  it('清除预设后 presetParams 一并清空', async () => {
    setNode({ promptPreset: 'portrait_tune', presetParams: { fusion: 'strong' } });
    await renderReady();
    await waitFor(() => expect(screen.getByText('人像质感调节')).toBeInTheDocument());

    await userEvent.click(screen.getByRole('button', { name: '移除预设' }));

    expect(settingsOf().promptPreset).toBeUndefined();
    expect(settingsOf().presetParams).toBeUndefined();
  });

  it('换预设时旧预设的 presetParams 被清（维度 key 各预设私有）', async () => {
    setNode({ promptPreset: 'portrait_tune', presetParams: { fusion: 'strong' } });
    await renderReady();
    await waitFor(() => expect(presetButton()).toBeInTheDocument());

    await userEvent.click(presetButton()!);
    await userEvent.click(screen.getByText('情绪版'));

    expect(settingsOf().promptPreset).toBe('emotion_board');
    expect(settingsOf().presetParams).toBeUndefined();
  });
});
