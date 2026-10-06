import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App as AntApp } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { VideoTrimBar } from './VideoTrimBar';
import { useCanvasStore } from '../../store';

// 媒体处理层与上传通道全 mock:这里测的是确认流程的编排——
// 转码 → 上传 → 建 preview 节点 + 连线 + kind 自报 → 退出 trim 模式。
const trimVideoMock = vi.fn();
vi.mock('./videoTrim', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./videoTrim')>()),
  extractFilmstrip: vi.fn().mockResolvedValue({
    duration: 5,
    frames: Array.from({ length: 12 }, () => 'data:image/jpeg;base64,F'),
  }),
  trimVideo: (...args: unknown[]) => trimVideoMock(...args),
}));

const uploadAssetMock = vi.fn();
vi.mock('@/shared/api', () => ({
  uploadAsset: (...args: unknown[]) => uploadAssetMock(...args),
  currentProjectId: () => 42,
}));

const persistNodeByIdMock = vi.fn();
vi.mock('../../persistence/canvasMutations', () => ({
  persistNodeById: (...args: unknown[]) => persistNodeByIdMock(...args),
}));

function setupStore() {
  useCanvasStore.setState({
    nodes: [
      {
        id: 'src-1',
        type: 'gen-video',
        x: 100,
        y: 50,
        // 故意用非 preview 默认(320×260)的尺寸:钉住「产物节点尺寸抄源节点」
        width: 480,
        height: 360,
        status: 'succeeded',
        content: 'http://x.mp4',
        settings: {},
      },
    ],
    connections: [],
    selectedIds: new Set(['src-1']),
    view: { x: 0, y: 0, zoom: 1 },
    lightbox: null,
    trimmingNodeId: 'src-1',
    readOnly: false,
  });
}

async function renderBarReady() {
  // AntApp:组件里 App.useApp() 的 message 需要 provider。
  // 带上宿主节点的 <video>:循环预览、I/O、Space 都经 data-node-id 找它。
  const utils = render(
    <AntApp>
      <div data-node-id="src-1">
        <video src="http://x.mp4" />
      </div>
      <VideoTrimBar nodeId="src-1" url="http://x.mp4" />
    </AntApp>,
  );
  // 抽帧完成后气泡出现(初始全选 5.00 s),代表进入 edit 态
  await screen.findByText('5.00 s');
  return utils;
}

describe('VideoTrimBar 确认流程', () => {
  beforeEach(() => {
    setupStore();
    trimVideoMock.mockReset().mockResolvedValue(new Blob(['x'], { type: 'video/mp4' }));
    uploadAssetMock.mockReset().mockResolvedValue({ id: 1, url: 'http://cdn/trimmed.mp4', size: 1, mimeType: 'video/mp4' });
    persistNodeByIdMock.mockReset();
    // jsdom 的媒体方法没有实现(play 返回 undefined,.catch 会炸),按原型 stub 掉
    window.HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
    window.HTMLMediaElement.prototype.pause = vi.fn();
  });

  it('确认:转码→上传→源右侧建 preview 节点(kind=video)并连线,退出 trim', async () => {
    await renderBarReady();

    await userEvent.click(screen.getByTitle('确认剪辑 (Enter)'));

    await waitFor(() => expect(useCanvasStore.getState().trimmingNodeId).toBeNull());
    // 转码收到全选选区,上传收到带时间戳命名的 mp4 文件
    expect(trimVideoMock).toHaveBeenCalledWith('http://x.mp4', 0, 5, expect.any(Function));
    const file = uploadAssetMock.mock.calls[0][0] as File;
    expect(file.name).toBe('trim-0.00s-5.00s.mp4');
    expect(uploadAssetMock.mock.calls[0][1]).toBe(42);

    const state = useCanvasStore.getState();
    const created = state.nodes.find((n) => n.type === 'preview');
    expect(created).toBeDefined();
    expect(created!.content).toBe('http://cdn/trimmed.mp4');
    expect(created!.settings?.kind).toBe('video');
    // 尺寸抄源节点、右侧留 80 间距、同 y 垂直对齐(剪辑不改画面,大小不该跳变)
    expect(created!.width).toBe(480);
    expect(created!.height).toBe(360);
    expect(created!.x).toBe(100 + 480 + 80);
    expect(created!.y).toBe(50);
    const conn = state.connections.find((c) => c.from === 'src-1' && c.to === created!.id);
    expect(conn).toBeDefined();
    expect(conn!.inputType).toBe('video');
    expect(persistNodeByIdMock).toHaveBeenCalledWith(created!.id);
  });

  it('上传失败:不建节点、trim 模式保持、可重试', async () => {
    uploadAssetMock.mockRejectedValueOnce(new Error('网络错误'));
    await renderBarReady();

    await userEvent.click(screen.getByTitle('确认剪辑 (Enter)'));

    // 回到 edit 态(确认按钮恢复可点)而不是卡在处理中
    await waitFor(() =>
      expect(screen.getByTitle('确认剪辑 (Enter)')).not.toBeDisabled(),
    );
    const state = useCanvasStore.getState();
    expect(state.nodes.find((n) => n.type === 'preview')).toBeUndefined();
    expect(state.trimmingNodeId).toBe('src-1');
  });

  it('Esc 取消退出 trim 模式,不产生任何节点', async () => {
    await renderBarReady();
    await userEvent.keyboard('{Escape}');
    const state = useCanvasStore.getState();
    expect(state.trimmingNodeId).toBeNull();
    expect(state.nodes).toHaveLength(1);
  });

  it('↑↓ 扩缩选区:默认 0.1s,Shift 精确 0.01s,Ctrl 快速 1s', async () => {
    await renderBarReady();
    // 初始全选 5.00,收缩是两端对称各动一步
    await userEvent.keyboard('{ArrowDown}');
    expect(screen.getByText('4.80 s')).toBeInTheDocument();
    await userEvent.keyboard('{Shift>}{ArrowDown}{/Shift}');
    expect(screen.getByText('4.78 s')).toBeInTheDocument();
    await userEvent.keyboard('{Control>}{ArrowDown}{/Control}');
    expect(screen.getByText('2.78 s')).toBeInTheDocument();
    // 扩展被钳在视频边界,回不到超过 5.00
    await userEvent.keyboard('{Control>}{ArrowUp}{ArrowUp}{/Control}');
    expect(screen.getByText('5.00 s')).toBeInTheDocument();
  });

  it('←→ 平移选区,保持时长', async () => {
    const { container } = await renderBarReady();
    await userEvent.keyboard('{ArrowDown}'); // 0.10..4.90,腾出平移空间
    await userEvent.keyboard('{ArrowRight}');
    // 0.20..5.00:选区窗口左缘 = 0.2/5 = 4%
    const win = container.querySelector<HTMLElement>('.cursor-grab')!;
    expect(parseFloat(win.style.left)).toBeCloseTo(4);
    expect(screen.getByText('4.80 s')).toBeInTheDocument();
  });

  it('I/O 把入点/出点设为当前播放位置', async () => {
    const { container } = await renderBarReady();
    const video = container.querySelector('video')!;
    video.currentTime = 2;
    await userEvent.keyboard('i');
    expect(screen.getByText('3.00 s')).toBeInTheDocument();
    video.currentTime = 4;
    await userEvent.keyboard('o');
    expect(screen.getByText('2.00 s')).toBeInTheDocument();
  });

  it('Space 在播放中按下会暂停循环预览', async () => {
    const { container } = await renderBarReady();
    const video = container.querySelector('video')!;
    Object.defineProperty(video, 'paused', { get: () => false, configurable: true });
    await userEvent.keyboard(' ');
    expect(video.pause).toHaveBeenCalled();
  });

  it('键盘按钮开合快捷键面板', async () => {
    await renderBarReady();
    await userEvent.click(screen.getByTitle('快捷键'));
    expect(screen.getByText('移动选区')).toBeInTheDocument();
    expect(screen.getByText('设置入点出点')).toBeInTheDocument();
    await userEvent.click(screen.getByTitle('快捷键'));
    expect(screen.queryByText('移动选区')).toBeNull();
  });

  it('吸附按钮在开/关标题间切换', async () => {
    await renderBarReady();
    await userEvent.click(screen.getByTitle('开启整数秒吸附'));
    expect(screen.getByTitle('关闭整数秒吸附')).toBeInTheDocument();
    await userEvent.click(screen.getByTitle('关闭整数秒吸附'));
    expect(screen.getByTitle('开启整数秒吸附')).toBeInTheDocument();
  });
});
