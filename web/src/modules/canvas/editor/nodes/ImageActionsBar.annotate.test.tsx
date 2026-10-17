// 标注交互约定钉死在这里：
// 点「标注」进入模式（模式条顶掉常规工具条）；✕ / ESC / 点节点外 = 直接丢弃
// 退出、无确认；保存把 sourceId + 已加载原图 + 笔画交给 executeAnnotateSave；
// 无图节点进入即警告退出。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ImageActionsBar } from './ImageActionsBar';
import { useCanvasStore } from '../store';
import { fetchGenImagePresets, fetchLightControlConfig, fetchMultiAngleConfig } from '@/shared/api';
import { executeAnnotateSave, loadAnnotateImage } from './annotate/executeAnnotateSave';

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    api: { post: vi.fn() },
    fetchGenImagePresets: vi.fn(),
    fetchLightControlConfig: vi.fn(),
    fetchMultiAngleConfig: vi.fn(),
  };
});

vi.mock('../persistence/canvasMutations', () => ({
  persistNodeById: vi.fn(),
  scheduleNodePatch: vi.fn(),
  flushNodePatch: vi.fn(),
}));

vi.mock('./annotate/executeAnnotateSave', () => ({
  executeAnnotateSave: vi.fn(),
  loadAnnotateImage: vi.fn(),
}));

const fakeImage = { naturalWidth: 1548, naturalHeight: 1016 } as HTMLImageElement;

function setStore(content = 'https://example.test/ref.png') {
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
        content,
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

async function enterAnnotate() {
  await userEvent.click(await screen.findByRole('button', { name: '标注' }));
  await screen.findByRole('button', { name: '保存标注' });
  // 冲掉 loadAnnotateImage 的 microtask，让 image 状态落定（保存按钮依赖它）。
  await act(async () => {});
}

describe('ImageActionsBar 标注', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchGenImagePresets).mockResolvedValue({ models: [], presets: [] });
    vi.mocked(fetchLightControlConfig).mockResolvedValue({ models: [] } as never);
    vi.mocked(fetchMultiAngleConfig).mockResolvedValue({ models: [] } as never);
    vi.mocked(loadAnnotateImage).mockResolvedValue(fakeImage);
    vi.mocked(executeAnnotateSave).mockResolvedValue('new-node-1');
    setStore();
  });

  it('点「标注」进入模式：模式条顶掉常规工具条，进入即预载原图', async () => {
    renderBar();
    await enterAnnotate();
    expect(screen.getByRole('button', { name: '退出标注' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '画笔' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '矩形' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '文字' })).toBeInTheDocument();
    // 常规工具条已被顶掉。
    expect(screen.queryByRole('button', { name: /宫格切分/ })).not.toBeInTheDocument();
    expect(loadAnnotateImage).toHaveBeenCalledWith('https://example.test/ref.png');
  });

  it('✕ 退出丢弃，回到常规工具条', async () => {
    renderBar();
    await enterAnnotate();
    await userEvent.click(screen.getByRole('button', { name: '退出标注' }));
    expect(screen.queryByRole('button', { name: '保存标注' })).not.toBeInTheDocument();
    expect(await screen.findByRole('button', { name: /宫格切分/ })).toBeInTheDocument();
  });

  it('ESC 退出丢弃', async () => {
    renderBar();
    await enterAnnotate();
    await userEvent.keyboard('{Escape}');
    expect(screen.queryByRole('button', { name: '保存标注' })).not.toBeInTheDocument();
  });

  it('点节点外任意处退出丢弃（无确认）', async () => {
    renderBar();
    await enterAnnotate();
    act(() => {
      // jsdom 未必有 PointerEvent 构造器；监听器只看 e.target，普通 Event 足够。
      window.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    });
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: '保存标注' })).not.toBeInTheDocument(),
    );
  });

  it('保存：sourceId + 已加载原图交给 executeAnnotateSave，成功后退出模式', async () => {
    renderBar();
    await enterAnnotate();
    await userEvent.click(screen.getByRole('button', { name: '保存标注' }));
    await waitFor(() => expect(executeAnnotateSave).toHaveBeenCalledTimes(1));
    expect(executeAnnotateSave).toHaveBeenCalledWith({
      sourceId: 'src-1',
      image: fakeImage,
      strokes: [],
    });
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: '保存标注' })).not.toBeInTheDocument(),
    );
  });

  it('合成失败（跨域等）留在模式内——笔画不丢', async () => {
    vi.mocked(executeAnnotateSave).mockRejectedValue(new Error('跨域限制'));
    renderBar();
    await enterAnnotate();
    await userEvent.click(screen.getByRole('button', { name: '保存标注' }));
    await waitFor(() => expect(executeAnnotateSave).toHaveBeenCalled());
    expect(screen.getByRole('button', { name: '保存标注' })).toBeInTheDocument();
  });

  it('标注模式镜像进 store：进入置 annotatingNodeId、退出清掉（gen-image 面板靠它让位）', async () => {
    renderBar();
    await enterAnnotate();
    expect(useCanvasStore.getState().annotatingNodeId).toBe('src-1');
    await userEvent.click(screen.getByRole('button', { name: '退出标注' }));
    expect(useCanvasStore.getState().annotatingNodeId).toBeNull();
  });

  it('无图节点：进入即退出，不进模式', async () => {
    setStore('');
    renderBar();
    await userEvent.click(await screen.findByRole('button', { name: '标注' }));
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: '保存标注' })).not.toBeInTheDocument(),
    );
    expect(loadAnnotateImage).not.toHaveBeenCalled();
  });
});
