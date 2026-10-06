// 各下载入口都接到了 saveToDisk 封装上（而不是 <a download>）：点下载 → 调封装 → 成功/失败有提示。
import { App as AntApp } from 'antd';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const saveMediaToDisk = vi.fn();
const saveBlobToDisk = vi.fn();
const saveManyMediaToDisk = vi.fn();
vi.mock('@/shared/saveToDisk', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./saveToDisk')>();
  return {
    ...actual,
    saveMediaToDisk: (...a: unknown[]) => saveMediaToDisk(...a),
    saveBlobToDisk: (...a: unknown[]) => saveBlobToDisk(...a),
    saveManyMediaToDisk: (...a: unknown[]) => saveManyMediaToDisk(...a),
  };
});

vi.mock('@/modules/canvas/editor/nodes/trim/videoTrim', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/modules/canvas/editor/nodes/trim/videoTrim')>();
  return { ...actual, isTrimSupported: () => true };
});

const listTasksMock = vi.fn();
const listRuntimeModelsMock = vi.fn();
vi.mock('@/shared/api', async () => {
  const actual = await vi.importActual<typeof import('@/shared/api')>('@/shared/api');
  return {
    ...actual,
    listTasks: (...args: unknown[]) => listTasksMock(...args),
    listRuntimeModels: (...args: unknown[]) => listRuntimeModelsMock(...args),
  };
});

import { useCanvasStore } from '@/modules/canvas/editor/store';
import { AudioActionsBar } from '@/modules/canvas/editor/nodes/AudioActionsBar';
import { Lightbox } from '@/modules/canvas/editor/Lightbox';
import { HistoryPanel } from '@/modules/canvas/panels/HistoryPanel';
import { downloadCaptureResults } from '@/modules/canvas/editor/nodes/director/desk/editor/io/screenshotExport';

const MEDIA = 'http://127.0.0.1:54321/media/gen/task-9-0?token=t';

beforeEach(() => {
  saveMediaToDisk.mockReset().mockResolvedValue('saved');
  saveBlobToDisk.mockReset().mockResolvedValue('saved');
  saveManyMediaToDisk.mockReset().mockResolvedValue(2);
});

describe('AudioActionsBar 下载', () => {
  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [
        { id: 'a1', type: 'input-audio', x: 0, y: 0, width: 300, height: 200, status: 'succeeded', content: MEDIA, settings: {} },
      ],
      connections: [],
      selectedIds: new Set(['a1']),
      view: { x: 0, y: 0, zoom: 1 },
      readOnly: false,
      audioEdit: null,
    } as never);
  });

  it('点下载调 saveMediaToDisk，成功提示已保存', async () => {
    render(
      <AntApp>
        <AudioActionsBar nodeId="a1" visible url={MEDIA} />
      </AntApp>,
    );
    fireEvent.click(screen.getByRole('button', { name: '下载' }));
    await waitFor(() => expect(saveMediaToDisk).toHaveBeenCalledTimes(1));
    const [url, name] = saveMediaToDisk.mock.calls[0];
    expect(url).toBe(MEDIA);
    expect(name).toMatch(/^audio-\d+\.mp3$/);
    expect(await screen.findByText('已保存')).toBeTruthy();
  });

  it('失败时报出原因，不再“点了没反应”', async () => {
    saveMediaToDisk.mockRejectedValue('文件不存在，可能已被删除');
    vi.spyOn(console, 'error').mockImplementation(() => {});
    render(
      <AntApp>
        <AudioActionsBar nodeId="a1" visible url={MEDIA} />
      </AntApp>,
    );
    fireEvent.click(screen.getByRole('button', { name: '下载' }));
    expect(await screen.findByText('保存失败：文件不存在，可能已被删除')).toBeTruthy();
  });
});

describe('Lightbox 下载', () => {
  it('下载按钮调 saveMediaToDisk，不再渲染 <a download>', async () => {
    useCanvasStore.setState({ lightbox: { url: MEDIA, kind: 'video', title: 't' } });
    const { container } = render(
      <AntApp>
        <Lightbox />
      </AntApp>,
    );
    expect(container.querySelector('a[download]')).toBeNull();
    fireEvent.click(screen.getByTitle('下载'));
    await waitFor(() => expect(saveMediaToDisk).toHaveBeenCalledTimes(1));
    expect(saveMediaToDisk.mock.calls[0][1]).toMatch(/^video-\d+\.mp4$/);
    useCanvasStore.setState({ lightbox: null });
  });
});

describe('HistoryPanel 下载', () => {
  beforeEach(() => {
    listTasksMock.mockReset().mockResolvedValue({
      data: [
        {
          id: 9,
          userId: 1,
          projectId: 1,
          modelId: 'm',
          status: 'succeeded',
          input: { inputs: {} },
          output: { outputUrls: [MEDIA], outputKind: 'image' },
          createdAt: '2026-10-01T00:00:00.000Z',
          updatedAt: '2026-10-01T00:00:05.000Z',
        },
      ],
      total: 1,
    });
    listRuntimeModelsMock.mockReset().mockResolvedValue([]);
    useCanvasStore.setState({ nodes: [], connections: [], lightbox: null, selectedIds: new Set() });
  });

  function renderPanel() {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(
      <AntApp>
        <QueryClientProvider client={qc}>
          <HistoryPanel open projectId={1} onClose={() => {}} />
        </QueryClientProvider>
      </AntApp>,
    );
  }

  it('右键下载走 saveMediaToDisk（本地媒体由 Rust 直接复制，不经前端 fetch）', async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal('fetch', fetchSpy);
    const { container } = renderPanel();
    const img = await waitFor(() => {
      const el = container.querySelector('img');
      expect(el).toBeTruthy();
      return el as HTMLImageElement;
    });
    fireEvent.contextMenu(img, { clientX: 5, clientY: 5 });
    fireEvent.click(await screen.findByText('下载'));
    await waitFor(() => expect(saveMediaToDisk).toHaveBeenCalledWith(MEDIA, 'task-9.png'));
    expect(fetchSpy).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });

  it('批量下载打 ZIP 后交给 saveBlobToDisk', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(new Uint8Array([1, 2]))));
    const { container } = renderPanel();
    await waitFor(() => expect(container.querySelector('img')).toBeTruthy());
    fireEvent.click(await screen.findByTitle('批量下载'));
    await waitFor(() => expect(saveBlobToDisk).toHaveBeenCalledTimes(1), { timeout: 5000 });
    const [blob, name] = saveBlobToDisk.mock.calls[0];
    expect(blob).toBeInstanceOf(Blob);
    expect(name).toMatch(/^history-\d+\.zip$/);
    vi.unstubAllGlobals();
  });
});

describe('导演台截图导出', () => {
  const shot = (label: string) => ({
    label,
    dataUrl: 'data:image/png;base64,AQID',
    meta: { mode: 'director' as const, cameraId: null, fov: 50, position: [0, 0, 0] as [number, number, number], target: [0, 0, 0] as [number, number, number] },
  });

  it('单张直接 saveMediaToDisk，多张打包成一个 ZIP 只弹一次', async () => {
    await expect(downloadCaptureResults([shot('a')])).resolves.toBe(1);
    expect(saveMediaToDisk).toHaveBeenCalledTimes(1);

    await expect(downloadCaptureResults([shot('a'), shot('b'), shot('c')])).resolves.toBe(3);
    expect(saveBlobToDisk).toHaveBeenCalledTimes(1);
    expect(saveBlobToDisk.mock.calls[0][1]).toMatch(/\.zip$/);

    saveBlobToDisk.mockResolvedValue('cancelled');
    await expect(downloadCaptureResults([shot('a'), shot('b')])).resolves.toBe(0);
  });
});
