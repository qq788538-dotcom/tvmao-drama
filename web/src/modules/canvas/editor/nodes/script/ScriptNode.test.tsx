import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useCanvasStore } from '../../store';
import { ScriptNode } from './ScriptNode';

// 生成器面板依赖网络 api 与落库调度，节点级测试只关心开合行为，全部 mock 掉。
vi.mock('../../persistence/canvasMutations', () => ({ scheduleNodePatch: vi.fn(), persistNodeById: vi.fn(), flushNodePatch: vi.fn().mockResolvedValue(undefined) }));
vi.mock('@/shared/api', () => ({
  getCanvasScriptStyles: vi.fn().mockResolvedValue({ styles: [{ name: '古风' }], modelCandidates: [] }),
  submitCanvasScriptTask: vi.fn(),
  getCanvasScriptTask: vi.fn(),
}));

function renderNode(id: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><ScriptNode id={id} /></QueryClientProvider>);
}

describe('ScriptNode', () => {
  beforeEach(() => {
    useCanvasStore.setState({ nodes: [], connections: [], selectedIds: new Set(), readOnly: false });
  });

  it('有分镜时渲染两步进度，点按钮打开面板', () => {
    useCanvasStore.setState({
      nodes: [{
        id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
        settings: { rows: [{ id: 'r1', prompt: 'p' }], assets: { characters: [], scenes: [], props: [] } },
      }],
    });
    renderNode('s1');
    expect(screen.getByText('确认分镜')).toBeInTheDocument();
    expect(screen.getByText('准备资产')).toBeInTheDocument();
    expect(screen.queryByText('合成提示词')).toBeNull();
    fireEvent.click(screen.getByTestId('script-open-panel'));
    expect(screen.getByTestId('script-panel')).toBeInTheDocument();
  });

  it('空态渲染「尝试」引导：剧本生成打开生成器且两入口隐藏（互斥）', () => {
    useCanvasStore.setState({
      nodes: [{ id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220, settings: {} }],
      selectedIds: new Set(['s1']),
    });
    renderNode('s1');
    expect(screen.queryByTestId('script-open-panel')).toBeNull();
    expect(screen.getByText('尝试：')).toBeInTheDocument();
    expect(screen.queryByTestId('script-generator')).toBeNull();
    fireEvent.click(screen.getByTestId('script-try-generate'));
    const generator = screen.getByTestId('script-generator');
    expect(generator).toBeInTheDocument();
    // 浮层溢出节点边界依赖 NodeShell body 的 overflow-visible；默认 hidden 会把
    // 面板裁没（jsdom 不做视觉裁剪，只能断言 class 链上没有裁剪容器）。
    let el: HTMLElement | null = generator;
    while (el) {
      expect(el.classList.contains('overflow-hidden')).toBe(false);
      el = el.parentElement;
    }
    // 生成模式下两个「尝试」入口整体隐藏——不能岔出「自己编写」第二条路
    expect(screen.queryByTestId('script-try-generate')).toBeNull();
    expect(screen.queryByTestId('script-try-manual')).toBeNull();
  });

  it('取消选中退出生成模式：入口恢复可再选「自己编写」', () => {
    useCanvasStore.setState({
      nodes: [{ id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220, settings: {} }],
      selectedIds: new Set(['s1']),
    });
    renderNode('s1');
    fireEvent.click(screen.getByTestId('script-try-generate'));
    expect(screen.getByTestId('script-generator')).toBeInTheDocument();
    act(() => {
      useCanvasStore.setState({ selectedIds: new Set() });
    });
    expect(screen.queryByTestId('script-generator')).toBeNull();
    fireEvent.click(screen.getByTestId('script-try-manual'));
    expect(screen.getByTestId('script-panel')).toBeInTheDocument();
  });

  it('有进行中任务时生成器面板恒显示（不依赖选中）', () => {
    useCanvasStore.setState({
      nodes: [{
        id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
        settings: { gen: { taskId: 3, style: '古风', durationSec: 10, error: '' } },
      }],
      selectedIds: new Set(),
    });
    renderNode('s1');
    expect(screen.getByTestId('script-generator')).toBeInTheDocument();
    expect(screen.getAllByText('分镜生成中…').length).toBeGreaterThan(0);
  });
});
