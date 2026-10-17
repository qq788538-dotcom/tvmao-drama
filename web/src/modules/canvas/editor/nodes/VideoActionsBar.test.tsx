import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App as AntApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { VideoActionsBar } from './VideoActionsBar';
import { useCanvasStore } from '../store';

// 组件里 App.useApp() 的 message 需要 <App> provider,测试统一包一层;
// 片段重拍配置走 useQuery,再包一层 QueryClientProvider(同 ImageActionsBar 测试)。
function renderBar(props: { nodeId: string; visible: boolean; url?: string }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <AntApp>
        <VideoActionsBar {...props} />
      </AntApp>
    </QueryClientProvider>,
  );
}

// trim 模式的裁剪条自身在 VideoTrimBar.test.tsx 里测;这里 stub 掉,免得它
// 挂载后真去 mediabunny 抽帧(jsdom 里只会异步报错制造噪音)。
vi.mock('./trim/VideoTrimBar', () => ({
  VideoTrimBar: () => <div data-testid="trim-bar" />,
}));

// 裁剪层同理 stub 掉:它挂载后会去量 <video> 内容区,这里的宿主没有视频元素,
// 真渲染会报「未找到视频」并把模式退掉,污染进入模式的断言。
vi.mock('./crop/VideoCropOverlay', () => ({
  VideoCropOverlay: () => <div data-testid="crop-overlay" />,
}));

// 重拍面板同理 stub 掉:挂载后会真去 mediabunny 抽帧。
vi.mock('./reshoot/SegmentReshootBar', () => ({
  SegmentReshootBar: () => <div data-testid="reshoot-bar" />,
}));

// 续写面板同理 stub 掉:挂载后会真去 mediabunny 抽帧。
vi.mock('./continuation/VideoContinuationBar', () => ({
  VideoContinuationBar: () => <div data-testid="continuation-bar" />,
}));

// 音视频分离的落地侧在 executeAudioSeparate.test.ts 里测;这里 stub 掉,
// 只断言菜单展开与调用参数。
const audioSeparateMock = vi.hoisted(() => vi.fn(async () => {}));
vi.mock('./audiosep/executeAudioSeparate', () => ({
  executeAudioSeparate: audioSeparateMock,
}));

// 重拍/续写的前置条件都是模型中心配了各自写死的模型;按测试用例切换目录内容。
const runtimeModelsMock = vi.hoisted(() =>
  vi.fn(async (): Promise<Array<{ modelId: string }>> => [
    { modelId: 'doubao-seedance-2-5-260628-edit' },
    { modelId: 'doubao-seedance-2-5-260628-extend' },
  ]),
);
vi.mock('@/shared/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/shared/api')>()),
  listRuntimeModels: runtimeModelsMock,
}));

// 「擦除字幕」「视频转高清」的 spawn 行为测试（入口在本工具条，不在右键菜单）。

function setupStore() {
  useCanvasStore.setState({
    nodes: [
      {
        id: 'src-1',
        type: 'gen-video',
        x: 100,
        y: 50,
        width: 320,
        height: 200,
        status: 'succeeded',
        content: 'http://x.mp4',
        settings: {},
      },
    ],
    connections: [],
    // 工具条只在「单选中本节点」时渲染。
    selectedIds: new Set(['src-1']),
    view: { x: 0, y: 0, zoom: 1 },
    lightbox: null,
    trimmingNodeId: null,
    croppingNodeId: null,
    readOnly: false,
  });
}

describe('VideoActionsBar 「智能去字幕」/「高清」', () => {
  beforeEach(setupStore);
  afterEach(() => vi.unstubAllGlobals());

  it('点击「智能去字幕」在源节点右侧新建 subtitle-erase 节点并以 video 连线', async () => {
    renderBar({ nodeId: 'src-1', visible: true });

    await userEvent.click(screen.getByText('智能去字幕'));

    const state = useCanvasStore.getState();
    const created = state.nodes.find((n) => n.type === 'subtitle-erase');
    expect(created).toBeDefined();
    // 新节点应落在源节点右侧（addNode 以传入点为中心点，所以只断言方向，不
    // 断言与 DEFAULT_SIZES 强耦合的精确坐标）。
    expect(created!.x).toBeGreaterThan(100);

    const conn = state.connections.find((c) => c.from === 'src-1' && c.to === created!.id);
    expect(conn).toBeDefined();
    expect(conn!.inputType).toBe('video');
  });

  it('点击「高清」在源节点右侧新建 video-upscale 节点并以 video 连线', async () => {
    renderBar({ nodeId: 'src-1', visible: true });

    await userEvent.click(screen.getByText('高清'));

    const state = useCanvasStore.getState();
    const created = state.nodes.find((n) => n.type === 'video-upscale');
    expect(created).toBeDefined();
    expect(created!.x).toBeGreaterThan(100);

    const conn = state.connections.find((c) => c.from === 'src-1' && c.to === created!.id);
    expect(conn).toBeDefined();
    expect(conn!.inputType).toBe('video');
  });

  it('点击「白模」在源节点右侧新建 video-whitemodel 节点并以 video 连线（不直接提交）', async () => {
    renderBar({ nodeId: 'src-1', visible: true });

    await userEvent.click(screen.getByText('白模'));

    const state = useCanvasStore.getState();
    const created = state.nodes.find((n) => n.type === 'video-whitemodel');
    expect(created).toBeDefined();
    expect(created!.x).toBeGreaterThan(100);
    // 与「高清」同形态：只铺节点，扣费动作留给节点里的「转白模」按钮。
    expect(created!.status ?? 'idle').toBe('idle');
    expect(created!.taskId).toBeUndefined();

    const conn = state.connections.find((c) => c.from === 'src-1' && c.to === created!.id);
    expect(conn).toBeDefined();
    expect(conn!.inputType).toBe('video');
  });

  it('「白模」按钮紧挨在「高清」右边', () => {
    renderBar({ nodeId: 'src-1', visible: true });
    const labels = screen.getAllByRole('button').map((b) => b.textContent?.trim());
    const hd = labels.findIndex((l) => l?.endsWith('高清'));
    expect(hd).toBeGreaterThanOrEqual(0);
    expect(labels[hd + 1]).toMatch(/白模$/);
  });

  it('点击「剪辑」进入 trim 模式,工具条让位给裁剪条', async () => {
    // jsdom 没有 WebCodecs,stub 一个让 isTrimSupported 放行
    vi.stubGlobal('VideoEncoder', class {});
    renderBar({ nodeId: 'src-1', visible: true, url: 'http://x.mp4' });

    await userEvent.click(screen.getByText('剪辑'));

    expect(useCanvasStore.getState().trimmingNodeId).toBe('src-1');
    expect(screen.getByTestId('trim-bar')).toBeInTheDocument();
    // 用常显按钮断言工具条已让位（占位按钮被开关隐藏，拿它们断言会假绿）。
    expect(screen.queryByText('智能去字幕')).toBeNull();
  });

  it('点击「裁剪」进入 crop 模式,工具条让位给裁剪层', async () => {
    vi.stubGlobal('VideoEncoder', class {});
    renderBar({ nodeId: 'src-1', visible: true, url: 'http://x.mp4' });

    await userEvent.click(screen.getByText('裁剪'));

    expect(useCanvasStore.getState().croppingNodeId).toBe('src-1');
    expect(screen.getByTestId('crop-overlay')).toBeInTheDocument();
    expect(screen.queryByText('智能去字幕')).toBeNull();
  });

  it('不支持 WebCodecs 时点「裁剪」不进入 crop 模式', async () => {
    renderBar({ nodeId: 'src-1', visible: true, url: 'http://x.mp4' });
    await userEvent.click(screen.getByText('裁剪'));
    expect(useCanvasStore.getState().croppingNodeId).toBeNull();
  });

  it('不支持 WebCodecs 时点「剪辑」不进入 trim 模式', async () => {
    renderBar({ nodeId: 'src-1', visible: true, url: 'http://x.mp4' });
    await userEvent.click(screen.getByText('剪辑'));
    expect(useCanvasStore.getState().trimmingNodeId).toBeNull();
  });

  // 两种后处理节点共用 spawnPostprocessNode 的落位算法（那段坐标计算曾因「偏上 +
  // 压住节点」修过一次），所以钉住它们落在同一位置——将来若有人只改一处就会挂。
  it('两种后处理节点落位一致：垂直居中于源节点', async () => {
    renderBar({ nodeId: 'src-1', visible: true });

    await userEvent.click(screen.getByText('智能去字幕'));
    // addNode 会把选中转移到新建节点，工具条随之隐藏（单选源节点才显示）；
    // 模拟用户重新点选源节点后再点第二个按钮。
    act(() => {
      useCanvasStore.setState({ selectedIds: new Set(['src-1']) });
    });
    await userEvent.click(screen.getByText('高清'));

    const nodes = useCanvasStore.getState().nodes;
    const erase = nodes.find((n) => n.type === 'subtitle-erase')!;
    const upscale = nodes.find((n) => n.type === 'video-upscale')!;
    expect(upscale.x).toBe(erase.x);
    expect(upscale.y).toBe(erase.y);
  });
});

describe('VideoActionsBar 「片段重拍」入口', () => {
  beforeEach(() => {
    setupStore();
    useCanvasStore.setState({ reshootNodeId: null });
    runtimeModelsMock.mockResolvedValue([{ modelId: 'doubao-seedance-2-5-260628-edit' }]);
  });
  afterEach(() => vi.unstubAllGlobals());

  it('点「音频分离」直接触发音视频分离(只有一项,无下拉)', async () => {
    vi.stubGlobal('VideoEncoder', class {});
    renderBar({ nodeId: 'src-1', visible: true, url: 'http://x.mp4' });

    await userEvent.click(screen.getByText('音频分离'));

    expect(audioSeparateMock).toHaveBeenCalledTimes(1);
    expect(audioSeparateMock).toHaveBeenCalledWith('src-1', 'http://x.mp4', expect.any(Function));
  });

  it('不支持 WebCodecs 时点「音频分离」不触发处理', async () => {
    audioSeparateMock.mockClear();
    renderBar({ nodeId: 'src-1', visible: true, url: 'http://x.mp4' });
    await userEvent.click(screen.getByText('音频分离'));
    expect(audioSeparateMock).not.toHaveBeenCalled();
  });

  it('模型中心配了重拍模型时点击进入重拍模式,工具条让位给面板', async () => {
    vi.stubGlobal('VideoEncoder', class {});
    renderBar({ nodeId: 'src-1', visible: true, url: 'http://x.mp4' });
    // 等模型目录查询落定(data 未就绪会走「加载中」分支)。
    await screen.findByText('片段重拍');
    await vi.waitFor(async () => {
      await userEvent.click(screen.getByText('片段重拍'));
      expect(useCanvasStore.getState().reshootNodeId).toBe('src-1');
    });
    expect(screen.getByTestId('reshoot-bar')).toBeInTheDocument();
    expect(screen.queryByText('智能去字幕')).toBeNull();
  });

  it('模型中心没配重拍模型时点击不进入重拍模式', async () => {
    vi.stubGlobal('VideoEncoder', class {});
    runtimeModelsMock.mockResolvedValue([{ modelId: 'other-model' }]);
    renderBar({ nodeId: 'src-1', visible: true, url: 'http://x.mp4' });
    // 等一拍让目录查询落定。
    await vi.waitFor(() => expect(runtimeModelsMock).toHaveBeenCalled());
    await userEvent.click(screen.getByText('片段重拍'));
    expect(useCanvasStore.getState().reshootNodeId).toBeNull();
  });

  it('不支持 WebCodecs 时点击不进入重拍模式', async () => {
    renderBar({ nodeId: 'src-1', visible: true, url: 'http://x.mp4' });
    await userEvent.click(screen.getByText('片段重拍'));
    expect(useCanvasStore.getState().reshootNodeId).toBeNull();
  });
});

describe('VideoActionsBar 「智能续写」入口', () => {
  beforeEach(() => {
    setupStore();
    useCanvasStore.setState({ continuationNodeId: null });
    runtimeModelsMock.mockResolvedValue([{ modelId: 'doubao-seedance-2-5-260628-extend' }]);
  });
  afterEach(() => vi.unstubAllGlobals());

  it('模型中心启用了续写模型时点击进入续写模式,工具条让位给面板', async () => {
    vi.stubGlobal('VideoEncoder', class {});
    renderBar({ nodeId: 'src-1', visible: true, url: 'http://x.mp4' });
    await screen.findByText('智能续写');
    // 等模型列表查询落定(data 未就绪会走「加载中」分支)。
    await vi.waitFor(async () => {
      await userEvent.click(screen.getByText('智能续写'));
      expect(useCanvasStore.getState().continuationNodeId).toBe('src-1');
    });
    expect(screen.getByTestId('continuation-bar')).toBeInTheDocument();
    expect(screen.queryByText('智能去字幕')).toBeNull();
  });

  it('模型中心没有续写模型时点击不进入续写模式', async () => {
    vi.stubGlobal('VideoEncoder', class {});
    runtimeModelsMock.mockResolvedValue([{ modelId: 'other-model' }]);
    renderBar({ nodeId: 'src-1', visible: true, url: 'http://x.mp4' });
    await vi.waitFor(() => expect(runtimeModelsMock).toHaveBeenCalled());
    await userEvent.click(screen.getByText('智能续写'));
    expect(useCanvasStore.getState().continuationNodeId).toBeNull();
  });

  it('不支持 WebCodecs 时点击不进入续写模式', async () => {
    renderBar({ nodeId: 'src-1', visible: true, url: 'http://x.mp4' });
    await userEvent.click(screen.getByText('智能续写'));
    expect(useCanvasStore.getState().continuationNodeId).toBeNull();
  });
});
