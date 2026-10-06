import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { InputImageNode } from './nodes/InputImageNode';
import { VideoInputNode } from './nodes/VideoInputNode';
import { AudioInputNode } from './nodes/AudioInputNode';
import { ContextMenu } from './ContextMenu';
import { useCanvasStore } from './store';
import {
  configureCanvasPersistence,
  flushCanvasMutations,
  resetCanvasPersistenceForTest,
  setCanvasOwnerToken,
} from './persistence/canvasMutations';
import { upsertCanvasNode, upsertCanvasConnection, uploadAsset } from '@/shared/api';
import { pickRefUploadForNode } from './utils/uploadRefPicker';
import type { NodeType } from './types';

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    // thumbs.ts 走 api.post 换缩略图——stub 掉，否则 jsdom 里发真实 XHR。
    api: { post: vi.fn() },
    uploadAsset: vi.fn(),
    upsertCanvasNode: vi.fn(),
    upsertCanvasConnection: vi.fn(),
    currentProjectId: vi.fn().mockReturnValue(42),
  };
});

vi.mock('./batchQueries', () => ({
  loadEstimate: vi.fn(),
  loadSeedanceStatus: vi.fn().mockResolvedValue(undefined),
}));

const assetURL = 'https://example.test/uploaded.png';

function seedNode(id: string, type: NodeType) {
  useCanvasStore.setState({
    nodes: [{ id, type, x: 0, y: 0, width: 260, height: 260, status: 'idle', settings: {} }],
    connections: [],
    selectedIds: new Set<string>(),
    view: { x: 0, y: 0, zoom: 1 },
    lightbox: null,
    readOnly: false,
  });
}

function withProviders(ui: React.ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

/** 找到节点里的文件选择器并塞一个文件——等同用户点「选择/更换」后选中文件。 */
async function pickFile(container: HTMLElement, file: File) {
  const input = container.querySelector('input[type="file"]') as HTMLInputElement;
  expect(input).not.toBeNull();
  await act(async () => {
    fireEvent.change(input, { target: { files: [file] } });
  });
  await act(async () => {
    await flushCanvasMutations();
  });
}

/** 断言上传的内容真的以「带 content 的完整节点」被持久化了。 */
function expectPersistedContent(nodeID: string) {
  const calls = vi.mocked(upsertCanvasNode).mock.calls.filter(([, , node]) => node.id === nodeID);
  expect(calls.length).toBeGreaterThan(0);
  expect(calls[calls.length - 1][2]).toMatchObject({ id: nodeID, content: assetURL });
}

describe('上传入口的持久化', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetCanvasPersistenceForTest();
    configureCanvasPersistence({ projectID: 42 });
    setCanvasOwnerToken('owner-token');
    vi.mocked(upsertCanvasNode).mockResolvedValue({ version: 1 });
    vi.mocked(uploadAsset).mockResolvedValue({
      id: 7,
      url: assetURL,
      mimeType: 'image/png',
    } as Awaited<ReturnType<typeof uploadAsset>>);
  });

  afterEach(() => {
    resetCanvasPersistenceForTest();
  });

  it('图片节点内「选择图片」上传后持久化 content', async () => {
    seedNode('img-1', 'input-image');
    const { container } = withProviders(<InputImageNode id="img-1" />);

    await pickFile(container, new File(['x'], 'a.png', { type: 'image/png' }));

    expect(useCanvasStore.getState().nodes[0].content).toBe(assetURL);
    expectPersistedContent('img-1');
  });

  it('视频节点内「选择视频」上传后持久化 content', async () => {
    seedNode('vid-1', 'video-input');
    const { container } = withProviders(<VideoInputNode id="vid-1" />);

    await pickFile(container, new File(['x'], 'a.mp4', { type: 'video/mp4' }));

    expectPersistedContent('vid-1');
  });

  it('音频节点内「选择音频」上传后持久化 content', async () => {
    seedNode('aud-1', 'input-audio');
    const { container } = withProviders(<AudioInputNode id="aud-1" />);

    await pickFile(container, new File(['x'], 'a.mp3', { type: 'audio/mpeg' }));

    expectPersistedContent('aud-1');
  });

  it('右键菜单「上传」建节点并上传后持久化 content', async () => {
    seedNode('seed', 'text-node');
    // handleUploadClick 用 document.createElement 造了个游离的 file input，
    // 拿不到 DOM 引用——截住 createElement 把它抓出来再喂文件。
    const created: HTMLInputElement[] = [];
    const realCreate = document.createElement.bind(document);
    const spy = vi.spyOn(document, 'createElement').mockImplementation(((tag: string, opts?: unknown) => {
      const el = realCreate(tag as 'input', opts as ElementCreationOptions);
      if (tag === 'input') created.push(el as HTMLInputElement);
      return el;
    }) as typeof document.createElement);

    const { getByText } = withProviders(
      <ContextMenu x={0} y={0} worldX={100} worldY={200} onClose={() => {}} />,
    );
    await act(async () => {
      fireEvent.click(getByText('上传'));
    });
    spy.mockRestore();

    const input = created[created.length - 1];
    expect(input).toBeDefined();
    Object.defineProperty(input, 'files', {
      value: [new File(['x'], 'a.png', { type: 'image/png' })],
    });
    await act(async () => {
      input.dispatchEvent(new Event('change'));
    });
    await act(async () => {
      await flushCanvasMutations();
    });

    const newNode = useCanvasStore.getState().nodes.find((n) => n.type === 'input-image');
    expect(newNode).toBeDefined();
    expectPersistedContent(newNode!.id);
  });

  it('上传失败也把 failed 状态持久化下去', async () => {
    seedNode('img-2', 'input-image');
    vi.mocked(uploadAsset).mockRejectedValue(new Error('boom'));
    const { container } = withProviders(<InputImageNode id="img-2" />);

    await pickFile(container, new File(['x'], 'a.png', { type: 'image/png' }));

    const calls = vi.mocked(upsertCanvasNode).mock.calls.filter(([, , n]) => n.id === 'img-2');
    expect(calls.length).toBeGreaterThan(0);
    expect(calls[calls.length - 1][2]).toMatchObject({ status: 'failed', error: 'boom' });
  });
});

describe('生成节点「+」的参考素材上传', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetCanvasPersistenceForTest();
    configureCanvasPersistence({ projectID: 42 });
    setCanvasOwnerToken('owner-token');
    vi.mocked(upsertCanvasNode).mockResolvedValue({ version: 1 });
    vi.mocked(upsertCanvasConnection).mockResolvedValue({ version: 1 });
    vi.mocked(uploadAsset).mockResolvedValue({
      id: 7,
      url: assetURL,
      mimeType: 'image/png',
    } as Awaited<ReturnType<typeof uploadAsset>>);
  });

  afterEach(() => {
    resetCanvasPersistenceForTest();
  });

  /** 与右键菜单测试同款：截住 createElement 抓出游离 file input 再喂文件。 */
  async function pickViaHelper(opts: Parameters<typeof pickRefUploadForNode>[0], file: File) {
    const created: HTMLInputElement[] = [];
    const realCreate = document.createElement.bind(document);
    const spy = vi.spyOn(document, 'createElement').mockImplementation(((tag: string, o?: unknown) => {
      const el = realCreate(tag as 'input', o as ElementCreationOptions);
      if (tag === 'input') created.push(el as HTMLInputElement);
      return el;
    }) as typeof document.createElement);
    pickRefUploadForNode(opts);
    spy.mockRestore();

    const input = created[created.length - 1];
    expect(input).toBeDefined();
    Object.defineProperty(input, 'files', { value: [file] });
    await act(async () => {
      input.dispatchEvent(new Event('change'));
    });
    await act(async () => {
      await flushCanvasMutations();
    });
  }

  it('gen-image 上传图片：建 input-image、连线指向 gen 节点、content 持久化', async () => {
    seedNode('gen-1', 'gen-image');
    const onRejected = vi.fn();

    await pickViaHelper(
      { targetId: 'gen-1', accept: 'image/*', allowed: ['input-image'], rejectHint: '仅图片', onRejected },
      new File(['x'], 'ref.png', { type: 'image/png' }),
    );

    const s = useCanvasStore.getState();
    const refNode = s.nodes.find((n) => n.type === 'input-image');
    expect(refNode).toBeDefined();
    // 连线方向必须是 参考素材 → 生成节点（参考图是上游），反了缩略图不出现。
    expect(s.connections).toContainEqual(
      expect.objectContaining({ from: refNode!.id, to: 'gen-1' }),
    );
    expectPersistedContent(refNode!.id);
    // 连线本身也要落库——只挂内存里刷新就断线。
    expect(upsertCanvasConnection).toHaveBeenCalled();
    expect(onRejected).not.toHaveBeenCalled();
  });

  it('gen-image 选到视频：拒收，不建节点不连线', async () => {
    seedNode('gen-2', 'gen-image');
    const onRejected = vi.fn();

    await pickViaHelper(
      { targetId: 'gen-2', accept: 'image/*', allowed: ['input-image'], rejectHint: 'AI 绘图仅支持图片', onRejected },
      new File(['x'], 'clip.mp4', { type: 'video/mp4' }),
    );

    expect(onRejected).toHaveBeenCalledWith('clip.mp4', 'AI 绘图仅支持图片');
    const s = useCanvasStore.getState();
    expect(s.nodes).toHaveLength(1);
    expect(s.connections).toHaveLength(0);
    expect(uploadAsset).not.toHaveBeenCalled();
  });

  it('gen-video 上传音频：建 input-audio 并连线', async () => {
    seedNode('gen-3', 'gen-video');

    await pickViaHelper(
      {
        targetId: 'gen-3',
        accept: 'image/*,audio/*,video/*',
        allowed: ['input-image', 'input-audio', 'video-input'],
        rejectHint: '仅图片/音频/视频',
        onRejected: vi.fn(),
      },
      new File(['x'], 'bgm.mp3', { type: 'audio/mpeg' }),
    );

    const s = useCanvasStore.getState();
    const refNode = s.nodes.find((n) => n.type === 'input-audio');
    expect(refNode).toBeDefined();
    expect(s.connections).toContainEqual(
      expect.objectContaining({ from: refNode!.id, to: 'gen-3' }),
    );
    expectPersistedContent(refNode!.id);
  });
});
