import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Sidebar } from './Sidebar';
import { useCanvasStore } from './store';
import {
  configureCanvasPersistence,
  flushCanvasMutations,
  resetCanvasPersistenceForTest,
  setCanvasOwnerToken,
} from './persistence/canvasMutations';
import { patchCanvasNodePositions } from '@/shared/api';
import type { CanvasNode } from './types';

// 自动整理的持久化回归测试。整理直接 setState 换 nodes，绕开了 store 的结构性
// op 广播（几何更新按设计不广播，见 store.ts CanvasOp 注释），所以必须像拖动
// 松手一样在调用点显式 PATCH。Sidebar 曾经漏掉这一步：点击当场排好，刷新即
// 回弹到整理前——上过生产的真实回归。

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    getToken: vi.fn().mockReturnValue(null),
    patchCanvasNodePositions: vi.fn(),
  };
});

function makeNode(id: string, x: number, y: number): CanvasNode {
  return { id, type: 'gen-image', x, y, width: 280, height: 280, settings: {}, status: 'idle' };
}

function renderSidebar() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const noop = () => {};
  return render(
    <QueryClientProvider client={queryClient}>
      <Sidebar
        projectId={42}
        activeTool="select"
        onToolChange={noop}
        historyOpen={false}
        onToggleHistory={noop}
        libraryOpen={false}
        onToggleLibrary={noop}
      />
    </QueryClientProvider>,
  );
}

describe('自动整理的持久化', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetCanvasPersistenceForTest();
    configureCanvasPersistence({ projectID: 42 });
    setCanvasOwnerToken('owner-token');
    vi.mocked(patchCanvasNodePositions).mockResolvedValue({ version: 1 });
    // 两个节点故意堆在同一坐标——整理必然会挪动其中至少一个
    useCanvasStore.setState({
      nodes: [makeNode('a', 0, 0), makeNode('b', 0, 0)],
      connections: [],
      selectedIds: new Set(['a', 'b']),
      undoStack: [],
      redoStack: [],
      readOnly: false,
    });
  });

  afterEach(() => {
    resetCanvasPersistenceForTest();
  });

  it('整理后按选中节点整体 PATCH 新坐标——不落库刷新即回弹', async () => {
    const { getByTitle } = renderSidebar();
    await act(async () => {
      fireEvent.click(getByTitle(/自动整理节点/));
      await flushCanvasMutations();
    });

    expect(patchCanvasNodePositions).toHaveBeenCalledTimes(1);
    const [pid, token, patches] = vi.mocked(patchCanvasNodePositions).mock.calls[0];
    expect(pid).toBe(42);
    expect(token).toBe('owner-token');
    // PATCH 的坐标必须就是整理后 store 里的坐标——两边不一致等于白存
    const stateNodes = new Map(useCanvasStore.getState().nodes.map((n) => [n.id, n]));
    expect(patches.map((p) => p.id).sort()).toEqual(['a', 'b']);
    for (const p of patches) {
      const n = stateNodes.get(p.id)!;
      expect(p).toMatchObject({ x: n.x, y: n.y, width: n.width, height: n.height });
    }
  });

  it('选中不足 2 个节点只提示，不发请求', async () => {
    useCanvasStore.setState({ selectedIds: new Set(['a']) });
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {});
    const { getByTitle } = renderSidebar();
    await act(async () => {
      fireEvent.click(getByTitle(/自动整理节点/));
      await flushCanvasMutations();
    });
    expect(alertSpy).toHaveBeenCalled();
    expect(patchCanvasNodePositions).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });
});
