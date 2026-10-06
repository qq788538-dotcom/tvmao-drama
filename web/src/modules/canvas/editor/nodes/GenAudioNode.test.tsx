import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { GenAudioNode } from './GenAudioNode';
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
    api: { post: vi.fn() },
    listRuntimeModels: vi.fn().mockResolvedValue([]),
    submitCanvasTask: vi.fn(),
    pollCanvasTaskUntilDone: vi.fn(),
    uploadAsset: vi.fn(),
  };
});

vi.mock('../batchQueries', () => ({
  loadSeedanceStatus: vi.fn().mockResolvedValue(undefined),
}));

const audioURL = 'https://example.test/gen/task-7-0';

const audioModel: ModelConfig = {
  id: 1,
  type: 'audio',
  name: 'TTS Model',
  modelId: 'tts-model',
  basePath: '',
  enabled: true,
  hidden: false,
  sortOrder: 1,
  requestTemplate: {},
  uiSchema: {
    voice: {
      label: '音色',
      default: 'female',
      options: [
        { value: 'female', label: '女声' },
        { value: 'male', label: '男声' },
      ],
    },
  },
};

const imageModel: ModelConfig = {
  ...audioModel,
  id: 2,
  type: 'image',
  name: 'Image Model',
  modelId: 'image-model',
};

function renderNode() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <GenAudioNode id="audio-1" />
    </QueryClientProvider>,
  );
}

describe('GenAudioNode', () => {
  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    vi.mocked(listRuntimeModels).mockResolvedValue([audioModel, imageModel]);
    vi.mocked(loadSeedanceStatus).mockResolvedValue(undefined);
    vi.mocked(api.post).mockResolvedValue({ data: { thumbs: {} } });
    useCanvasStore.setState({
      nodes: [{
        id: 'audio-1',
        type: 'gen-audio',
        x: 0,
        y: 0,
        width: 340,
        height: 180,
        status: 'idle',
        settings: {},
      }],
      connections: [],
      selectedIds: new Set(['audio-1']),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  });

  it('选中后不显示估价或积分文案（X3）', async () => {
    vi.mocked(listRuntimeModels).mockResolvedValue([{ ...audioModel, priceNote: '100积分/分钟' }, imageModel]);
    renderNode();

    expect((await screen.findAllByText('TTS Model', {}, { timeout: 3000 })).length).toBeGreaterThan(0);
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 700));
    });
    expect(screen.queryByText(/积分|估算中|预计/)).toBeNull();
  });

  it('模型选择器只列音频模型，图片模型不出现', async () => {
    renderNode();

    await userEvent.click(await screen.findByRole('button', { name: /TTS Model/ }));

    expect(screen.getAllByText('TTS Model').length).toBeGreaterThan(1);
    expect(screen.queryByText('Image Model')).toBeNull();
  });

  it('生成完成后节点内直接渲染波形播放器', async () => {
    useCanvasStore.setState({
      nodes: [{
        id: 'audio-1',
        type: 'gen-audio',
        x: 0,
        y: 0,
        width: 340,
        height: 180,
        status: 'succeeded',
        content: audioURL,
        settings: { history: [audioURL], historyIndex: 0 },
      }],
    });

    const { container } = renderNode();

    // WaveformPlayer：播放本体是隐藏的 <audio preload="metadata">（点播放前不拉
    // 音频本体），可见 UI 是波形 + 播放按钮。
    const audio = container.querySelector(`audio[src="${audioURL}"]`);
    expect(audio).not.toBeNull();
    expect(audio!.getAttribute('preload')).toBe('metadata');
    expect(screen.getByRole('button', { name: '播放' })).toBeTruthy();
  });

  it('提交的 inputs 是音频形态：带 audios/audio，不带 size/width/height', async () => {
    vi.mocked(submitCanvasTask).mockResolvedValue({ taskId: 42, status: 'processing' });
    vi.mocked(pollCanvasTaskUntilDone).mockResolvedValue({
      status: 'succeeded',
      outputUrls: [audioURL],
    });
    const voiceURL = 'https://example.test/voice.mp3';
    useCanvasStore.setState({
      nodes: [
        {
          id: 'audio-1',
          type: 'gen-audio',
          x: 0, y: 0, width: 340, height: 180,
          status: 'idle',
          settings: { prompt: '一段轻快的口播', modelId: 'tts-model' },
        },
        {
          id: 'voice-1',
          type: 'input-audio',
          x: 0, y: 0, width: 300, height: 200,
          status: 'succeeded',
          content: voiceURL,
        },
      ],
      connections: [{ id: 'c1', from: 'voice-1', to: 'audio-1' }],
      selectedIds: new Set(['audio-1']),
    });

    renderNode();
    await userEvent.click(await screen.findByRole('button', { name: '生成' }));

    await waitFor(() => expect(submitCanvasTask).toHaveBeenCalled());
    const submitted = vi.mocked(submitCanvasTask).mock.calls[0][0];
    expect(submitted.modelId).toBe('tts-model');
    expect(submitted.inputs.audios).toEqual([voiceURL]);
    expect(submitted.inputs.audio).toBe(voiceURL);
    expect(submitted.inputs.prompt).toContain('一段轻快的口播');
    // 音频没有画面——绝不能把图片那套分辨率键带给上游。
    expect(submitted.inputs).not.toHaveProperty('size');
    expect(submitted.inputs).not.toHaveProperty('width');
    expect(submitted.inputs).not.toHaveProperty('height');
    // uiSchema 的 voice 字段按默认值带上。
    expect(submitted.inputs.voice).toBe('female');
  });

  it('gen-audio 产物（无扩展名 URL）连给下游按音频归类', () => {
    useCanvasStore.setState({
      nodes: [
        {
          id: 'audio-1',
          type: 'gen-audio',
          x: 0, y: 0, width: 340, height: 180,
          status: 'succeeded',
          content: audioURL,
        },
        {
          id: 'video-1',
          type: 'gen-video',
          x: 0, y: 0, width: 400, height: 500,
          status: 'idle',
        },
      ],
      connections: [{ id: 'c1', from: 'audio-1', to: 'video-1' }],
    });

    const got = useCanvasStore.getState().getConnectedInputs('video-1');
    expect(got.audios).toEqual([audioURL]);
    expect(got.images).toEqual([]);
  });
});
