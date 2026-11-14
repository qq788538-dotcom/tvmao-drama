import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useCanvasStore } from '../../store';
import { ScriptGenerator, useScriptGenPolling } from './ScriptGenerator';

vi.mock('../../persistence/canvasMutations', () => ({ scheduleNodePatch: vi.fn(), flushNodePatch: vi.fn().mockResolvedValue(undefined) }));
vi.mock('@/shared/api', () => ({
  getCanvasScriptStyles: vi.fn(),
  submitCanvasScriptTask: vi.fn(),
  getCanvasScriptTask: vi.fn(),
}));
import { getCanvasScriptStyles, getCanvasScriptTask, submitCanvasScriptTask } from '@/shared/api';
import { flushNodePatch, scheduleNodePatch } from '../../persistence/canvasMutations';

function renderWithQuery(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

function nodeSettings(): Record<string, unknown> {
  return useCanvasStore.getState().nodes.find((n) => n.id === 's1')?.settings ?? {};
}

describe('ScriptGenerator', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(getCanvasScriptStyles).mockResolvedValue({
      styles: [{ name: '古风' }, { name: '现代' }],
      modelCandidates: [
        { modelId: 'm1', displayName: '快速版', credits: 30 },
        { modelId: 'm2', displayName: '深度版', credits: 50 },
      ],
    });
    useCanvasStore.setState({
      nodes: [{ id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220, settings: {} }],
      connections: [], selectedIds: new Set(), readOnly: false,
    });
  });

  it('空输入禁用提交；填剧本后提交任务并把 taskId 落 settings', async () => {
    vi.mocked(submitCanvasScriptTask).mockResolvedValue({ taskId: 7, status: 'pending' });
    renderWithQuery(<ScriptGenerator nodeId="s1" />);
    await waitFor(() => expect(screen.getByText('古风')).toBeInTheDocument());
    expect(screen.queryByText('30')).toBeNull();
    const submit = screen.getByTestId('script-generator-submit');
    expect(submit).toBeDisabled();

    fireEvent.change(screen.getByTestId('script-generator-input'), { target: { value: '一个武侠故事' } });
    expect(submit).not.toBeDisabled();
    fireEvent.click(submit);

    await waitFor(() => {
      expect(submitCanvasScriptTask).toHaveBeenCalledWith(
        expect.objectContaining({ nodeId: 's1', style: '古风', durationSec: 15, script: '一个武侠故事', model: 'm1' }),
      );
    });
    const gen = nodeSettings().gen as { taskId: number };
    expect(gen.taskId).toBe(7);
    // 任务进行中：显示生成中，不再显示提交按钮
    expect(screen.getByText('分镜生成中…')).toBeInTheDocument();
    expect(screen.queryByTestId('script-generator-submit')).toBeNull();
  });

  it('切换模型候选后提交 payload 的 model 同步更新，且不显示积分', async () => {
    vi.mocked(submitCanvasScriptTask).mockResolvedValue({ taskId: 12, status: 'pending' });
    const { container } = renderWithQuery(<ScriptGenerator nodeId="s1" />);
    await waitFor(() => expect(screen.getByText('古风')).toBeInTheDocument());
    expect(screen.queryByText('30')).toBeNull();

    // rc-select 在 `.ant-select-selector` 上监听 mousedown 展开下拉（不是外层
    // aria-label 容器）；风格 Cascader 排第一，秒数 Select 第二，模型 Select 第三。
    const modelSelector = container.querySelectorAll('.ant-select-selector')[2];
    fireEvent.mouseDown(modelSelector);
    const option = await screen.findByTitle('深度版');
    fireEvent.click(option);
    await waitFor(() => expect(modelSelector).toHaveTextContent('深度版'));
    expect(screen.queryByText('50')).toBeNull();

    fireEvent.change(screen.getByTestId('script-generator-input'), { target: { value: '剧本' } });
    fireEvent.click(screen.getByTestId('script-generator-submit'));
    await waitFor(() => {
      expect(submitCanvasScriptTask).toHaveBeenCalledWith(expect.objectContaining({ model: 'm2' }));
    });
  });

  it('modelCandidates 为空时提交按钮禁用', async () => {
    vi.mocked(getCanvasScriptStyles).mockResolvedValue({
      styles: [{ name: '古风' }],
      modelCandidates: [],
    });
    renderWithQuery(<ScriptGenerator nodeId="s1" />);
    await waitFor(() => expect(screen.getByText('古风')).toBeInTheDocument());
    fireEvent.change(screen.getByTestId('script-generator-input'), { target: { value: '剧本' } });
    expect(screen.getByTestId('script-generator-submit')).toBeDisabled();
  });

  it('节点 settings 预置 gen.model=m2 时选择器回显深度版，不被默认候选覆盖', async () => {
    useCanvasStore.setState({
      nodes: [{
        id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
        settings: { gen: { taskId: 0, style: '古风', model: 'm2', durationSec: 15, error: '' } },
      }],
      connections: [], selectedIds: new Set(), readOnly: false,
    });
    renderWithQuery(<ScriptGenerator nodeId="s1" />);
    await waitFor(() => expect(screen.getByText('深度版')).toBeInTheDocument());
    expect(screen.queryByText('50')).toBeNull();
  });

  it('节点 settings 预置 gen.model 指向已不在候选里的 slug 时回落第一候选', async () => {
    useCanvasStore.setState({
      nodes: [{
        id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
        settings: { gen: { taskId: 0, style: '古风', model: 'stale-model', durationSec: 15, error: '' } },
      }],
      connections: [], selectedIds: new Set(), readOnly: false,
    });
    renderWithQuery(<ScriptGenerator nodeId="s1" />);
    await waitFor(() => expect(screen.getByText('快速版')).toBeInTheDocument());
    expect(screen.queryByText('30')).toBeNull();
  });

  it('节点 settings 预置 gen.model 非空但候选列表为空时提交按钮禁用', async () => {
    vi.mocked(getCanvasScriptStyles).mockResolvedValue({
      styles: [{ name: '古风' }],
      modelCandidates: [],
    });
    useCanvasStore.setState({
      nodes: [{
        id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
        settings: { gen: { taskId: 0, style: '古风', model: 'stale-model', durationSec: 15, error: '' } },
      }],
      connections: [], selectedIds: new Set(), readOnly: false,
    });
    renderWithQuery(<ScriptGenerator nodeId="s1" />);
    await waitFor(() => expect(screen.getByText('古风')).toBeInTheDocument());
    fireEvent.change(screen.getByTestId('script-generator-input'), { target: { value: '剧本' } });
    expect(screen.getByTestId('script-generator-submit')).toBeDisabled();
  });

  it('带版本的风格提交 "name version" 全串（对齐后端 StyleEntry.Key）', async () => {
    vi.mocked(getCanvasScriptStyles).mockResolvedValue({
      styles: [{ name: '真人短剧', version: 'v2' }],
      modelCandidates: [{ modelId: 'm1', displayName: '快速版', credits: 30 }],
    });
    vi.mocked(submitCanvasScriptTask).mockResolvedValue({ taskId: 8, status: 'pending' });
    renderWithQuery(<ScriptGenerator nodeId="s1" />);
    await waitFor(() => expect(screen.getByText('真人短剧')).toBeInTheDocument());
    fireEvent.change(screen.getByTestId('script-generator-input'), { target: { value: '剧本' } });
    fireEvent.click(screen.getByTestId('script-generator-submit'));
    await waitFor(() => {
      expect(submitCanvasScriptTask).toHaveBeenCalledWith(
        expect.objectContaining({ style: '真人短剧 v2' }),
      );
    });
  });

  it('上游文字节点内容作为剧本来源：输入框留空也能提交，拼接串在前', async () => {
    vi.mocked(submitCanvasScriptTask).mockResolvedValue({ taskId: 11, status: 'pending' });
    useCanvasStore.setState({
      nodes: [
        { id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220, settings: {} },
        { id: 't1', type: 'text-node', x: -400, y: 0, width: 220, height: 160, settings: { text: '上游剧本内容' } },
      ],
      connections: [{ id: 'c1', from: 't1', to: 's1' }],
      selectedIds: new Set(), readOnly: false,
    });
    renderWithQuery(<ScriptGenerator nodeId="s1" />);
    await waitFor(() => expect(screen.getByText('古风')).toBeInTheDocument());
    expect(screen.getByTestId('script-generator-upstream')).toHaveTextContent('已接入上游文字节点');
    const submit = screen.getByTestId('script-generator-submit');
    expect(submit).not.toBeDisabled();
    fireEvent.change(screen.getByTestId('script-generator-input'), { target: { value: '补充说明' } });
    fireEvent.click(submit);
    await waitFor(() => {
      expect(submitCanvasScriptTask).toHaveBeenCalledWith(
        expect.objectContaining({ script: '上游剧本内容\n\n补充说明' }),
      );
    });
  });

  it('合并剧本超过 5000 字禁用提交并标红字数', async () => {
    useCanvasStore.setState({
      nodes: [
        { id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220, settings: {} },
        { id: 't1', type: 'text-node', x: -400, y: 0, width: 220, height: 160, settings: { text: '长'.repeat(4999) } },
      ],
      connections: [{ id: 'c1', from: 't1', to: 's1' }],
      selectedIds: new Set(), readOnly: false,
    });
    renderWithQuery(<ScriptGenerator nodeId="s1" />);
    await waitFor(() => expect(screen.getByText('古风')).toBeInTheDocument());
    // 上游 4999 + 输入 10 + 分隔符 2 > 5000
    fireEvent.change(screen.getByTestId('script-generator-input'), { target: { value: '超限的补充文本' } });
    expect(screen.getByTestId('script-generator-submit')).toBeDisabled();
    expect(screen.getByTestId('script-generator-count').className).toContain('text-red-400');
  });

  it('提交失败把错误落 settings.gen.error 并展示', async () => {
    vi.mocked(submitCanvasScriptTask).mockRejectedValue({ response: { data: { error: '积分余额不足，请充值后重试' } } });
    renderWithQuery(<ScriptGenerator nodeId="s1" />);
    await waitFor(() => expect(screen.getByText('古风')).toBeInTheDocument());
    fireEvent.change(screen.getByTestId('script-generator-input'), { target: { value: '剧本' } });
    fireEvent.click(screen.getByTestId('script-generator-submit'));
    await waitFor(() => {
      expect(screen.getByTestId('script-generator-error')).toHaveTextContent('积分余额不足');
    });
  });

  // P13：点画布空白处取消选中时 textarea 不失焦、面板直接卸载，草稿必须已经在 store 里。
  it('输入即写 store 并只对 scriptPrompt 排防抖落库，不失焦直接卸载草稿也不丢', async () => {
    const view = renderWithQuery(<ScriptGenerator nodeId="s1" />);
    await waitFor(() => expect(screen.getByText('古风')).toBeInTheDocument());
    vi.mocked(scheduleNodePatch).mockClear();

    fireEvent.change(screen.getByTestId('script-generator-input'), { target: { value: '半句剧本' } });
    expect(nodeSettings().scriptPrompt).toBe('半句剧本');
    expect(scheduleNodePatch).toHaveBeenCalledTimes(1);
    expect(scheduleNodePatch).toHaveBeenCalledWith('s1', { settings: { scriptPrompt: '半句剧本' } });

    view.unmount();
    renderWithQuery(<ScriptGenerator nodeId="s1" />);
    expect(screen.getByTestId('script-generator-input')).toHaveValue('半句剧本');
  });

  it('失焦时立即 flush 落库', async () => {
    renderWithQuery(<ScriptGenerator nodeId="s1" />);
    await waitFor(() => expect(screen.getByText('古风')).toBeInTheDocument());
    const input = screen.getByTestId('script-generator-input');
    fireEvent.change(input, { target: { value: '剧本' } });
    fireEvent.blur(input);
    expect(flushNodePatch).toHaveBeenCalledWith('s1');
  });
});

describe('useScriptGenPolling', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  function Probe({ nodeId }: { nodeId: string }) {
    useScriptGenPolling(nodeId);
    return null;
  }

  it('succeeded 后导入 candidate 并清 taskId', async () => {
    useCanvasStore.setState({
      nodes: [{
        id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
        settings: { gen: { taskId: 9, style: '古风', durationSec: 10, error: '' } },
      }],
      connections: [], selectedIds: new Set(), readOnly: false,
    });
    vi.mocked(getCanvasScriptTask).mockResolvedValue({
      status: 'succeeded', nodeId: 's1', durationSec: 10,
      result: JSON.stringify({
        character: [{ name: '张三', prompt: '侠客' }],
        storyboard: [{ name: '开场', duration: 6, prompt: '镜头1：@[角色-张三]' }],
      }),
    });
    render(<Probe nodeId="s1" />);
    await waitFor(() => {
      const s = nodeSettings() as { gen?: { taskId: number }; rows?: unknown[] };
      expect(s.gen?.taskId).toBe(0);
      expect(s.rows).toHaveLength(1);
    });
    const s = nodeSettings() as { assets: { characters: Array<{ name: string }> } };
    expect(s.assets.characters[0].name).toBe('张三');
  });

  it('failed 后清 taskId 并记录错误', async () => {
    useCanvasStore.setState({
      nodes: [{
        id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
        settings: { gen: { taskId: 9, style: '古风', durationSec: 10, error: '' } },
      }],
      connections: [], selectedIds: new Set(), readOnly: false,
    });
    vi.mocked(getCanvasScriptTask).mockResolvedValue({
      status: 'failed', nodeId: 's1', durationSec: 10, error: '上游超时',
    });
    render(<Probe nodeId="s1" />);
    await waitFor(() => {
      const s = nodeSettings() as { gen?: { taskId: number; error: string } };
      expect(s.gen?.taskId).toBe(0);
      expect(s.gen?.error).toBe('上游超时');
    });
  });

  function seedRunning(taskId = 9) {
    useCanvasStore.setState({
      nodes: [{
        id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
        settings: {
          rows: [{ id: 'r1', name: '已有镜头', duration: 5, prompt: '原有内容' }],
          gen: { taskId, style: '古风', durationSec: 10, error: '' },
        },
      }],
      connections: [], selectedIds: new Set(), readOnly: false,
    });
  }

  // P11：存量副本里带着别的节点的 taskId，状态响应的 nodeId 对不上就不认领。
  it('任务属于别的节点时清 taskId，不导入、不覆盖已有内容', async () => {
    seedRunning();
    vi.mocked(getCanvasScriptTask).mockResolvedValue({
      status: 'succeeded', nodeId: 'other', durationSec: 10,
      result: JSON.stringify({ storyboard: [{ name: '别人的', duration: 6, prompt: 'x' }] }),
    });
    render(<Probe nodeId="s1" />);
    await waitFor(() => {
      expect((nodeSettings().gen as { taskId: number }).taskId).toBe(0);
    });
    const s = nodeSettings() as { rows: Array<{ name: string }>; gen: { error: string } };
    expect(s.rows.map((r) => r.name)).toEqual(['已有镜头']);
    expect(s.gen.error).toBe('');
  });

  describe('错误判死', () => {
    beforeEach(() => vi.useFakeTimers());
    afterEach(() => vi.useRealTimers());

    const notFound = { response: { status: 404 } };
    const unauthorized = { response: { status: 401 } };

    // P12：任务行没了（404）不能永久转圈；连续 3 次才判死，写错误、清 taskId 以便重提。
    it('连续 3 次 404 判死：清 taskId 并写错误', async () => {
      seedRunning();
      vi.mocked(getCanvasScriptTask).mockRejectedValue(notFound);
      render(<Probe nodeId="s1" />);

      await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
      expect((nodeSettings().gen as { taskId: number }).taskId).toBe(9);

      await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
      const gen = nodeSettings().gen as { taskId: number; error: string };
      expect(getCanvasScriptTask).toHaveBeenCalledTimes(3);
      expect(gen.taskId).toBe(0);
      expect(gen.error).toBe('生成任务已不存在，请重新提交');
    });

    it('401/网络错误不计数，一直重试', async () => {
      seedRunning();
      vi.mocked(getCanvasScriptTask)
        .mockRejectedValueOnce(unauthorized)
        .mockRejectedValueOnce(new Error('Network Error'))
        .mockRejectedValueOnce(unauthorized)
        .mockRejectedValueOnce(new Error('Network Error'))
        .mockRejectedValueOnce(unauthorized);
      render(<Probe nodeId="s1" />);

      await act(async () => { await vi.advanceTimersByTimeAsync(12_000); });
      expect(getCanvasScriptTask).toHaveBeenCalledTimes(5);
      const gen = nodeSettings().gen as { taskId: number; error: string };
      expect(gen.taskId).toBe(9);
      expect(gen.error).toBe('');
    });

    it('404 之间夹着正常响应时计数清零', async () => {
      seedRunning();
      vi.mocked(getCanvasScriptTask)
        .mockRejectedValueOnce(notFound)
        .mockRejectedValueOnce(notFound)
        .mockResolvedValueOnce({ status: 'running', nodeId: 's1', durationSec: 10 })
        .mockRejectedValueOnce(notFound)
        .mockRejectedValueOnce(notFound);
      render(<Probe nodeId="s1" />);

      await act(async () => { await vi.advanceTimersByTimeAsync(12_000); });
      expect(getCanvasScriptTask).toHaveBeenCalledTimes(5);
      expect((nodeSettings().gen as { taskId: number }).taskId).toBe(9);
    });
  });
});
