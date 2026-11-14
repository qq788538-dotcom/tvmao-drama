import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { api } from '@/shared/api';
import { useCanvasStore } from '../store';
import { useReconcileCanvasTask } from './useReconcileCanvasTask';
import { clearPollStalled, markPollStalled, usePollStallStore } from './pollStall';

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

  // P31:同一节点在画布和故事板详情里各挂一份,只让一份发请求。
  it('同一节点挂两份:只有先挂的那份发请求,它卸载后另一份接手', async () => {
    const spy = vi.spyOn(api, 'get').mockRejectedValue(new Error('Network Error'));

    const first = renderHook(() => useReconcileCanvasTask('g1', opts));
    renderHook(() => useReconcileCanvasTask('g1', opts));
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(spy).toHaveBeenCalledTimes(1);

    first.unmount();
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(spy).toHaveBeenCalledTimes(2);
  });
});

// P23:主轮询连续够不着后端后挂起(pollStalled),交给这里低频对账。
describe('useReconcileCanvasTask 挂起对账', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    window.history.pushState({}, '', '/canvas/44');
    usePollStallStore.setState({ stalled: new Set() });
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
          taskId: 9,
          settings: {},
        },
      ],
      connections: [],
      readOnly: false,
    });
    markPollStalled(9);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  const opts = { initialDelayMs: 100, intervalMs: 1000, maxMisses: 4, maxStalledChecks: 3 };
  const stalled = () => usePollStallStore.getState().stalled.has(9);
  const node = () => useCanvasStore.getState().nodes.find((n) => n.id === 'g1');

  it('后端答话(任务仍在跑)就清挂起标记,交还主轮询;不改节点', async () => {
    const spy = vi.spyOn(api, 'get').mockResolvedValue({ data: { status: 'processing' } });

    renderHook(() => useReconcileCanvasTask('g1', opts));
    // 挂起对账不走 5s 首查,按间隔来——主轮询刚连续失败过。
    await act(() => vi.advanceTimersByTimeAsync(999));
    expect(spy).not.toHaveBeenCalled();
    await act(() => vi.advanceTimersByTimeAsync(1));

    expect(spy).toHaveBeenCalledWith('/canvas-tasks/9', undefined);
    expect(stalled()).toBe(false);
    expect(node()?.status).toBe('generating');
    expect(node()?.taskId).toBe(9);
  });

  it('够不着不判失败;到轮数上限就停,只留手动重新检查', async () => {
    const spy = vi.spyOn(api, 'get').mockRejectedValue(new Error('Network Error'));

    renderHook(() => useReconcileCanvasTask('g1', opts));
    await act(() => vi.advanceTimersByTimeAsync(10_000));

    expect(spy).toHaveBeenCalledTimes(3);
    expect(stalled()).toBe(true);
    expect(node()?.status).toBe('generating');
    expect(persistNodeById).not.toHaveBeenCalled();
  });

  it('连续多次 404:任务确实不存在,判死落库并清挂起', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(notFound());

    renderHook(() => useReconcileCanvasTask('g1', { ...opts, maxStalledChecks: 20 }));
    await act(() => vi.advanceTimersByTimeAsync(4000));

    expect(node()?.status).toBe('failed');
    expect(node()?.taskId).toBeUndefined();
    expect(persistNodeById).toHaveBeenCalledWith('g1');
    expect(stalled()).toBe(false);
  });

  it('未挂起的进行中任务不对账(主轮询在管)', async () => {
    clearPollStalled(9);
    const spy = vi.spyOn(api, 'get');

    renderHook(() => useReconcileCanvasTask('g1', opts));
    await act(() => vi.advanceTimersByTimeAsync(5000));

    expect(spy).not.toHaveBeenCalled();
  });
});
