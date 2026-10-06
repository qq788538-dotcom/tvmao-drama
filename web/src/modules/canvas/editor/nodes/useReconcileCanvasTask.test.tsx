import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { api } from '@/shared/api';
import { useCanvasStore } from '../store';
import { useReconcileCanvasTask } from './useReconcileCanvasTask';

vi.mock('../persistence/canvasMutations', () => ({ persistNodeById: vi.fn() }));
import { persistNodeById } from '../persistence/canvasMutations';

// 「永久生成中」死态的自愈对账：节点卡在 generating 且本地 taskId 丢失
// (提交响应没回到页面/落库竞态+刷新)时,按 nodeId 低频找回任务号交还现有
// 轮询;连续多轮查无(任务根本没提交成功)才判死,别让节点无限转圈。

const notFound = () => Object.assign(new Error('not found'), { response: { status: 404 } });

describe('useReconcileCanvasTask', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    window.history.pushState({}, '', '/canvas/44');
    useCanvasStore.setState({
      nodes: [
        {
          id: 'g1',
          type: 'gen-video',
          x: 0,
          y: 0,
          width: 280,
          height: 280,
          status: 'generating',
          settings: {},
        },
      ],
      connections: [],
      readOnly: false,
    });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  const opts = { initialDelayMs: 1000, intervalMs: 1000, maxMisses: 4 };

  it('generating 且无 taskId:按 nodeId 找回任务号写入节点', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({ data: { taskId: 833, status: 'succeeded' } });

    renderHook(() => useReconcileCanvasTask('g1', opts));
    await act(() => vi.advanceTimersByTimeAsync(1000));

    expect(api.get).toHaveBeenCalledWith('/canvas-tasks/by-node', {
      params: { projectId: 44, nodeId: 'g1' },
    });
    const node = useCanvasStore.getState().nodes.find((n) => n.id === 'g1');
    expect(node?.taskId).toBe(833);
  });

  it('已有 taskId 时不对账(现有轮询自会接管)', async () => {
    useCanvasStore.setState({
      nodes: [{ ...useCanvasStore.getState().nodes[0], taskId: 7 }],
    });
    const spy = vi.spyOn(api, 'get');

    renderHook(() => useReconcileCanvasTask('g1', opts));
    await act(() => vi.advanceTimersByTimeAsync(5000));

    expect(spy).not.toHaveBeenCalled();
  });

  it('非 generating 节点不对账', async () => {
    useCanvasStore.setState({
      nodes: [{ ...useCanvasStore.getState().nodes[0], status: 'succeeded' }],
    });
    const spy = vi.spyOn(api, 'get');

    renderHook(() => useReconcileCanvasTask('g1', opts));
    await act(() => vi.advanceTimersByTimeAsync(5000));

    expect(spy).not.toHaveBeenCalled();
  });

  it('连续 4 轮查无:判死节点并落库,不再无限转圈', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(notFound());

    renderHook(() => useReconcileCanvasTask('g1', opts));
    await act(() => vi.advanceTimersByTimeAsync(4000));

    const node = useCanvasStore.getState().nodes.find((n) => n.id === 'g1');
    expect(node?.status).toBe('failed');
    expect(persistNodeById).toHaveBeenCalledWith('g1');
  });

  it('网络错误不计入查无,不误判死', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(new Error('Network Error'));

    renderHook(() => useReconcileCanvasTask('g1', opts));
    await act(() => vi.advanceTimersByTimeAsync(8000));

    const node = useCanvasStore.getState().nodes.find((n) => n.id === 'g1');
    expect(node?.status).toBe('generating');
  });
});
