// 裁剪交互层的行为约定:量出内容区后进入编辑态(裁剪框+像素标签+8手柄)、
// Esc/取消退出模式、确认走 cropVideo→uploadAsset→spawnPostprocessNode 建
// preview 产物节点(content/kind/尺寸按裁剪比例缩)、失败保留框位可重试。
// jsdom 没有布局与视频解码:getBoundingClientRect / videoWidth 都打桩,
// cropVideo 打桩(编码路径与 trim 同理不在 jsdom 测)。
import { App as AntApp } from 'antd';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { VideoCropOverlay } from './VideoCropOverlay';
import { useCanvasStore } from '../../store';

const cropVideoMock = vi.fn();
vi.mock('./videoCrop', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./videoCrop')>();
  return { ...actual, cropVideo: (...a: unknown[]) => cropVideoMock(...a) };
});

const uploadAssetMock = vi.fn();
vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    uploadAsset: (...a: unknown[]) => uploadAssetMock(...a),
    currentProjectId: () => 42,
  };
});

vi.mock('../../persistence/canvasMutations', () => ({
  persistNodeById: vi.fn(),
  scheduleNodePatch: vi.fn(),
  flushNodePatch: vi.fn(),
}));

function setStore() {
  useCanvasStore.setState({
    nodes: [
      {
        id: 'src-1',
        type: 'gen-video' as const,
        x: 100,
        y: 50,
        width: 350,
        height: 622,
        status: 'succeeded' as const,
        content: 'http://x.mp4',
        settings: {},
      },
    ],
    connections: [],
    selectedIds: new Set(['src-1']),
    view: { x: 0, y: 0, zoom: 1 },
    readOnly: false,
    croppingNodeId: 'src-1',
    lightbox: null,
  });
}

// 宿主 DOM:overlay 经 [data-node-id] 找 <video> 量内容区。
function renderOverlay() {
  return render(
    <AntApp>
      <div data-node-id="src-1">
        <video src="http://x.mp4" />
        <VideoCropOverlay nodeId="src-1" url="http://x.mp4" />
      </div>
    </AntApp>,
  );
}

describe('VideoCropOverlay', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setStore();
    // jsdom 视频无解码:桩出 720×1280 的源像素。
    Object.defineProperty(HTMLVideoElement.prototype, 'videoWidth', {
      get: () => 720,
      configurable: true,
    });
    Object.defineProperty(HTMLVideoElement.prototype, 'videoHeight', {
      get: () => 1280,
      configurable: true,
    });
    // jsdom 无布局:所有盒子都当 350×622(与视频同比),内容区≈全幅。
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      x: 0, y: 0, left: 0, top: 0, right: 350, bottom: 622, width: 350, height: 622,
      toJSON: () => ({}),
    } as DOMRect);
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('量出内容区后进入编辑态:像素标签 576 x 1024、8 个手柄', async () => {
    renderOverlay();
    // 初始框 80% × (720,1280) = 576×1024
    expect(await screen.findAllByText('576 x 1024')).not.toHaveLength(0);
    for (const k of ['nw', 'n', 'ne', 'e', 'se', 's', 'sw', 'w']) {
      expect(screen.getByRole('button', { name: `resize-${k}` })).toBeInTheDocument();
    }
  });

  it('点取消退出 crop 模式,不产生节点', async () => {
    renderOverlay();
    await screen.findAllByText('576 x 1024');
    await userEvent.click(screen.getByRole('button', { name: '取消 (Esc)' }));
    expect(useCanvasStore.getState().croppingNodeId).toBeNull();
    expect(useCanvasStore.getState().nodes).toHaveLength(1);
  });

  it('确认:cropVideo 收偶数像素 rect,产物落 preview 节点(content/kind/按比例缩尺寸/连线)', async () => {
    cropVideoMock.mockResolvedValue(new Blob(['x'], { type: 'video/mp4' }));
    uploadAssetMock.mockResolvedValue({ url: 'http://cropped.mp4' });
    renderOverlay();
    await screen.findAllByText('576 x 1024');
    await userEvent.click(screen.getByRole('button', { name: '生成裁剪 (Enter)' }));

    await waitFor(() => expect(useCanvasStore.getState().croppingNodeId).toBeNull());
    expect(cropVideoMock).toHaveBeenCalledTimes(1);
    expect(cropVideoMock.mock.calls[0][0]).toBe('http://x.mp4');
    expect(cropVideoMock.mock.calls[0][1]).toEqual({ left: 72, top: 128, width: 576, height: 1024 });

    const state = useCanvasStore.getState();
    const created = state.nodes.find((n) => n.type === 'preview');
    expect(created).toBeDefined();
    expect(created!.content).toBe('http://cropped.mp4');
    expect(created!.settings?.kind).toBe('video');
    // 尺寸按裁剪比例缩:350×0.8=280、622×0.8≈498(1024/1280=0.8)
    expect(created!.width).toBe(280);
    expect(created!.height).toBe(498);
    // 落源节点右侧且连线
    expect(created!.x).toBeGreaterThan(100);
    const conn = state.connections.find((c) => c.from === 'src-1' && c.to === created!.id);
    expect(conn).toBeDefined();
  });

  it('转码失败:留在编辑态可重试,不建节点', async () => {
    cropVideoMock.mockRejectedValue(new Error('转码未产出数据'));
    renderOverlay();
    await screen.findAllByText('576 x 1024');
    await userEvent.click(screen.getByRole('button', { name: '生成裁剪 (Enter)' }));

    expect(await screen.findByText('转码未产出数据')).toBeInTheDocument();
    expect(useCanvasStore.getState().croppingNodeId).toBe('src-1');
    expect(useCanvasStore.getState().nodes).toHaveLength(1);
    // 编辑态恢复:确认按钮可再点
    expect(screen.getByRole('button', { name: '生成裁剪 (Enter)' })).not.toBeDisabled();
  });
});
