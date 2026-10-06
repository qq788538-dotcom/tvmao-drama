// 截取/变速落地侧的行为约定:上传后落 preview 音频节点(kind='audio')连回源,
// 反复操作的产物竖向排队;上传失败不落节点。
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { deliverAudioEditProduct } from './executeAudioEdit';
import { useCanvasStore } from '../../store';

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
        type: 'input-audio' as const,
        x: 100,
        y: 50,
        width: 300,
        height: 200,
        status: 'succeeded' as const,
        content: 'http://x.mp3',
        settings: {},
      },
    ],
    connections: [],
    selectedIds: new Set(['src-1']),
    view: { x: 0, y: 0, zoom: 1 },
    readOnly: false,
  });
}

describe('deliverAudioEditProduct', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setStore();
    uploadAssetMock.mockImplementation((file: File) =>
      Promise.resolve({ id: 7, url: `http://up/${file.name}` }),
    );
  });

  it('落 preview 音频节点连回源,位于源右侧', async () => {
    await deliverAudioEditProduct('src-1', new Blob(['a']), 'audio-trim');

    const st = useCanvasStore.getState();
    const product = st.nodes.find((n) => n.type === 'preview')!;
    expect(product).toBeTruthy();
    expect(product.content).toBe('http://up/audio-trim.wav');
    expect(product.settings?.kind).toBe('audio');
    expect(product.x).toBe(100 + 300 + 80);
    expect(product.y).toBe(50);
    expect(st.connections.some((c) => c.from === 'src-1' && c.to === product.id)).toBe(true);
  });

  it('反复操作的产物竖向排队,不精确压在上一个上', async () => {
    await deliverAudioEditProduct('src-1', new Blob(['a']), 'audio-trim');
    await deliverAudioEditProduct('src-1', new Blob(['b']), 'audio-speed-2x');

    const ys = useCanvasStore
      .getState()
      .nodes.filter((n) => n.type === 'preview')
      .map((n) => n.y)
      .sort((a, b) => a - b);
    expect(ys).toHaveLength(2);
    expect(ys[1]).toBeGreaterThan(ys[0]);
  });

  it('上传失败不落节点', async () => {
    uploadAssetMock.mockRejectedValue(new Error('upload failed'));
    await expect(deliverAudioEditProduct('src-1', new Blob(['a']), 'x')).rejects.toThrow('upload failed');
    expect(useCanvasStore.getState().nodes).toHaveLength(1);
    expect(useCanvasStore.getState().connections).toHaveLength(0);
  });
});
