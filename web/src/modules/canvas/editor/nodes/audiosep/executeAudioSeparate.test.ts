// 音视频分离落地侧的行为约定:拆轨产物双上传后,画布落 音频+无声视频 两个
// preview 节点(kind 各自自报/无声视频抄源尺寸/同排布局),都连回源;
// 拆轨失败(含无音轨)不落任何节点。转码本体在 videoAudioSeparate 里,
// 依赖 WebCodecs,jsdom 不测(同 trim/crop 先例)。
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { executeAudioSeparate } from './executeAudioSeparate';
import { useCanvasStore } from '../../store';

const separateMock = vi.fn();
vi.mock('./videoAudioSeparate', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./videoAudioSeparate')>();
  return { ...actual, separateAudioVideo: (...a: unknown[]) => separateMock(...a) };
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
  });
}

describe('executeAudioSeparate', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setStore();
  });

  it('产物落 音频+无声视频 两个 preview 节点,连回源,同排布局', async () => {
    separateMock.mockResolvedValue({
      audio: new Blob(['a'], { type: 'audio/wav' }),
      video: new Blob(['v'], { type: 'video/mp4' }),
    });
    uploadAssetMock.mockImplementation((file: File) =>
      Promise.resolve({ url: `http://up/${file.name}` }),
    );

    await executeAudioSeparate('src-1', 'http://x.mp4');

    const st = useCanvasStore.getState();
    const previews = st.nodes.filter((n) => n.type === 'preview');
    expect(previews).toHaveLength(2);

    const audio = previews.find((n) => n.settings?.kind === 'audio')!;
    const video = previews.find((n) => n.settings?.kind === 'video')!;
    expect(audio.content).toBe('http://up/audio-track.wav');
    expect(video.content).toBe('http://up/muted-video.mp4');
    // 源(100,50,350宽) → 音轨(x=100+350+80=530,默认 320 宽) → 无声(x=530+320+80=930)
    expect(audio.x).toBe(530);
    expect(audio.y).toBe(50);
    expect(video.x).toBe(930);
    expect(video.y).toBe(50);
    // 无声视频画面没变,抄源尺寸
    expect(video.width).toBe(350);
    expect(video.height).toBe(622);

    const conns = st.connections.filter((c) => c.from === 'src-1');
    expect(conns.map((c) => c.to).sort()).toEqual([audio.id, video.id].sort());
  });

  it('拆轨失败(含无音轨)不落任何节点、不上传', async () => {
    separateMock.mockRejectedValue(new Error('该视频没有音轨,无法分离'));

    await expect(executeAudioSeparate('src-1', 'http://x.mp4')).rejects.toThrow('没有音轨');
    expect(uploadAssetMock).not.toHaveBeenCalled();
    expect(useCanvasStore.getState().nodes).toHaveLength(1);
    expect(useCanvasStore.getState().connections).toHaveLength(0);
  });

  it('上传失败同样不落节点', async () => {
    separateMock.mockResolvedValue({
      audio: new Blob(['a'], { type: 'audio/wav' }),
      video: new Blob(['v'], { type: 'video/mp4' }),
    });
    uploadAssetMock.mockRejectedValue(new Error('upload failed'));

    await expect(executeAudioSeparate('src-1', 'http://x.mp4')).rejects.toThrow('upload failed');
    expect(useCanvasStore.getState().nodes).toHaveLength(1);
  });
});
