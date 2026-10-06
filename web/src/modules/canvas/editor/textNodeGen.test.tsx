import { act, fireEvent, render, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TextNode } from './nodes/TextNode';
import { useCanvasStore } from './store';
import {
  configureCanvasPersistence,
  resetCanvasPersistenceForTest,
  setCanvasOwnerToken,
} from './persistence/canvasMutations';
import {
  listRuntimeModels,
  patchCanvasNode,
  pollCanvasTaskUntilDone,
  submitCanvasTask,
  upsertCanvasNode,
} from '@/shared/api';

// 文本节点 AI 生成链路（前端侧）：提交带显式 modelId（铁律：服务端绝不替调用方
// 挑模型）、上游图片自动进 inputs、轮询终态把 text 写回 settings.text。
// 后端权威写回由 canvasgen sink 测试覆盖（text_writeback_test.go），这里只管
// 「页面开着时」的即时展示路径。

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    api: { post: vi.fn(), get: vi.fn().mockRejectedValue({ response: { status: 404 } }) },
    patchCanvasNode: vi.fn(),
    upsertCanvasNode: vi.fn(),
    currentProjectId: vi.fn().mockReturnValue(42),
    listRuntimeModels: vi.fn(),
    submitCanvasTask: vi.fn(),
    pollCanvasTaskUntilDone: vi.fn(),
  };
});

function renderNode(id: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <TextNode id={id} />
    </QueryClientProvider>,
  );
}

function seedTextNode(id: string, settings: Record<string, unknown> = {}) {
  useCanvasStore.setState({
    nodes: [{ id, type: 'text-node', x: 0, y: 0, width: 260, height: 200, status: 'idle', settings }],
    connections: [],
    selectedIds: new Set<string>([id]),
    view: { x: 0, y: 0, zoom: 1 },
    lightbox: null,
    readOnly: false,
  });
}

describe('文本节点 AI 生成', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetCanvasPersistenceForTest();
    configureCanvasPersistence({ projectID: 42 });
    setCanvasOwnerToken('owner-token');
    vi.mocked(patchCanvasNode).mockResolvedValue({ version: 1 });
    vi.mocked(upsertCanvasNode).mockResolvedValue({ version: 1 });
    vi.mocked(listRuntimeModels).mockResolvedValue([
      {
        id: 1,
        type: 'text',
        name: 'Qwen VL',
        modelId: 'qwen-vl',
        basePath: '/v1/chat/completions',
        enabled: true,
        hidden: false,
        sortOrder: 0,
        requestTemplate: {},
      },
    ] as Awaited<ReturnType<typeof listRuntimeModels>>);
    // 生成路径:提交拿 taskId,轮询回文本终态。
    vi.mocked(submitCanvasTask).mockResolvedValue({ taskId: 7, status: 'processing' });
    vi.mocked(pollCanvasTaskUntilDone).mockResolvedValue({ status: 'succeeded', text: '# 反推的提示词' });
    // TextGenPanel 从 URL 取 projectId(与 GenImageNode 同款)。
    window.history.pushState({}, '', '/canvas/42');
  });

  afterEach(() => {
    resetCanvasPersistenceForTest();
  });

  it('提交带显式 modelId 与上游图片，终态把 text 写回 settings.text', async () => {
    seedTextNode('t-1', { genPrompt: '根据图片，帮我生成提示词' });
    // 连一张上游图片节点:提交时必须作为视觉输入带上。
    useCanvasStore.setState((s) => ({
      nodes: [
        ...s.nodes,
        {
          id: 'img-1',
          type: 'input-image' as const,
          x: 0,
          y: 0,
          width: 100,
          height: 100,
          status: 'idle' as const,
          content: 'https://cdn.example/a.png',
          settings: {},
        },
      ],
      connections: [{ id: 'c1', from: 'img-1', to: 't-1' }],
    }));
    const { findByTitle } = renderNode('t-1');

    const submitBtn = await findByTitle('生成');
    await waitFor(() => expect(submitBtn).not.toBeDisabled());
    await act(async () => {
      fireEvent.click(submitBtn);
    });

    await waitFor(() => expect(submitCanvasTask).toHaveBeenCalledTimes(1));
    const call = vi.mocked(submitCanvasTask).mock.calls[0][0];
    expect(call.modelId).toBe('qwen-vl');
    expect(call.nodeId).toBe('t-1');
    expect(call.projectId).toBe(42);
    expect(call.inputs).toMatchObject({
      prompt: '根据图片，帮我生成提示词',
      image: 'https://cdn.example/a.png',
      images: ['https://cdn.example/a.png'],
    });

    // 轮询终态 → settings.text 即时更新,状态收敛 succeeded。
    await waitFor(() => {
      const n = useCanvasStore.getState().nodes.find((x) => x.id === 't-1')!;
      expect(n.settings?.text).toBe('# 反推的提示词');
      expect(n.status).toBe('succeeded');
      expect(n.taskId).toBeUndefined();
    });
  });

  it('无指令时生成按钮禁用——绝不空提交扣积分', async () => {
    seedTextNode('t-2');
    const { findByTitle } = renderNode('t-2');
    const submitBtn = await findByTitle('生成');
    expect(submitBtn).toBeDisabled();
    expect(submitCanvasTask).not.toHaveBeenCalled();
  });

  it('进入编辑态后生成输入条收起，退出编辑恢复', async () => {
    seedTextNode('t-4');
    const { findByTitle, getByText, queryByTitle } = renderNode('t-4');
    // 菜单态：生成条在
    expect(await findByTitle('生成')).toBeTruthy();
    // 点「自己编写内容」进入编辑 → 生成条收起（手写与 AI 生成互斥，
    // 同时摆着生成回来会覆盖正在手打的内容）
    fireEvent.click(getByText('自己编写内容'));
    await waitFor(() => {
      expect(document.querySelector('.tiptap-text-editor')).toBeTruthy();
      expect(queryByTitle('生成')).toBeNull();
    });
    // 编辑器失焦退出编辑 → 生成条恢复
    fireEvent.blur(document.querySelector('.tiptap-text-editor')!);
    await waitFor(() => {
      expect(queryByTitle('生成')).toBeTruthy();
    });
  });

  it('生成中节点显示状态提示且不可进入编辑', async () => {
    seedTextNode('t-3', { text: '旧内容' });
    useCanvasStore.setState((s) => ({
      nodes: s.nodes.map((n) => (n.id === 't-3' ? { ...n, status: 'generating' as const, taskId: 7 } : n)),
    }));
    // 轮询挂起,保持 generating 展示。
    vi.mocked(pollCanvasTaskUntilDone).mockReturnValue(new Promise(() => {}));
    const { findByTestId, queryByTestId } = renderNode('t-3');
    expect(await findByTestId('text-node-generating')).toBeTruthy();
    expect(queryByTestId('text-node-view')).toBeNull();
  });
});
