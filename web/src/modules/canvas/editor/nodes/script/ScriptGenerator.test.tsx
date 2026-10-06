import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useCanvasStore } from '../../store';
import { ScriptGenerator, useScriptGenPolling } from './ScriptGenerator';

vi.mock('../../persistence/canvasMutations', () => ({ scheduleNodePatch: vi.fn() }));
vi.mock('@/shared/api', () => ({
  getCanvasScriptStyles: vi.fn(),
  submitCanvasScriptTask: vi.fn(),
  getCanvasScriptTask: vi.fn(),
}));
import { getCanvasScriptStyles, getCanvasScriptTask, submitCanvasScriptTask } from '@/shared/api';

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
    expect(screen.getByText('30')).toBeInTheDocument();
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

  it('切换模型候选后 ⚡ 积分与提交 payload 的 model 同步更新', async () => {
    vi.mocked(submitCanvasScriptTask).mockResolvedValue({ taskId: 12, status: 'pending' });
    const { container } = renderWithQuery(<ScriptGenerator nodeId="s1" />);
    await waitFor(() => expect(screen.getByText('古风')).toBeInTheDocument());
    expect(screen.getByText('30')).toBeInTheDocument();

    // rc-select 在 `.ant-select-selector` 上监听 mousedown 展开下拉（不是外层
    // aria-label 容器）；风格 Cascader 排第一，秒数 Select 第二，模型 Select 第三。
    const modelSelector = container.querySelectorAll('.ant-select-selector')[2];
    fireEvent.mouseDown(modelSelector);
    const option = await screen.findByTitle('深度版');
    fireEvent.click(option);
    await waitFor(() => expect(screen.getByText('50')).toBeInTheDocument());

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
    expect(screen.getByText('50')).toBeInTheDocument();
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
    expect(screen.getByText('30')).toBeInTheDocument();
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
});
