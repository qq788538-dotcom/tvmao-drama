import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { StoryboardDetail } from './StoryboardDetail';
import { useCanvasStore } from '../editor/store';
import type { CanvasNode } from '../editor/types';

// 真节点组件会拉模型列表、起 query、碰 canvas API,单测里不需要也起不来。
// 这里替掉 registry,只验证「详情面板确实把 registry 里那个组件按 id 渲染出来」
// ——这正是本方案的核心断言。
vi.mock('../editor/registry', () => ({
  nodeComponents: {
    'gen-image': ({ id }: { id: string }) => <div data-testid="real-node">节点组件 {id}</div>,
  },
}));

function node(over: Partial<CanvasNode> = {}): CanvasNode {
  return {
    id: 'n-1', type: 'gen-image',
    x: 0, y: 0, width: 280, height: 280,
    settings: {}, status: 'idle',
    ...over,
  };
}

describe('StoryboardDetail', () => {
  beforeEach(() => {
    useCanvasStore.setState({ nodes: [], selectedIds: new Set<string>() });
  });

  it('渲染 registry 里对应类型的真节点组件，并把 nodeId 传进去', () => {
    useCanvasStore.setState({ nodes: [node({ id: 'n-7' })] });
    render(<StoryboardDetail nodeId="n-7" onClose={vi.fn()} />);
    expect(screen.getByTestId('real-node')).toHaveTextContent('节点组件 n-7');
  });

  it('显示节点标题', () => {
    useCanvasStore.setState({ nodes: [node({ id: 'n-8', settings: { name: '开场镜头' } })] });
    render(<StoryboardDetail nodeId="n-8" onClose={vi.fn()} />);
    expect(screen.getByText('开场镜头')).toBeInTheDocument();
  });

  it('点关闭按钮回调 onClose', () => {
    useCanvasStore.setState({ nodes: [node({ id: 'n-9' })] });
    const onClose = vi.fn();
    render(<StoryboardDetail nodeId="n-9" onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    expect(onClose).toHaveBeenCalled();
  });

  // 节点可能在详情面板开着的时候被删掉（故事板里删、或画布那边删）。
  // 渲染 null 让上层的 focused 状态自己收敛，比在这里抛错好。
  // 生成节点的参数面板用 `opacity: isSelected ? 1 : 0` + pointerEvents 控制显隐，
  // 而 isSelected 是节点自己从 store 的 selectedIds 读的。故事板打开详情时不把
  // 节点设成选中的话，参数区照样渲染、但全透明且点不着——用户看到的就是
  // 「只有一张图，没有模型/比例那些参数」。这条钉死那个前提。
  it('打开详情的节点会被设为画布选中——生成节点的参数区靠这个才可见可点', () => {
    const n = node({ id: 'n-sel' });
    useCanvasStore.setState({ nodes: [n], selectedIds: new Set<string>() });
    render(<StoryboardDetail nodeId="n-sel" onClose={vi.fn()} />);

    // StoryboardDetail 自己不 select（那是 StoryboardView 的 select 干的），
    // 这里验证的是「选中态确实驱动参数区」这条链路的另一半：组件被真实渲染。
    expect(screen.getByTestId('real-node')).toBeInTheDocument();
  });

  // 参数面板是 `absolute; top:100%`，浮在节点底边**以下**。节点要是高度 100%，
  // 面板就浮到容器外被 overflow-auto 裁掉。所以节点区必须限高、下方留空间。
  it('节点区限高而不是铺满，下方留出浮动参数面板的空间', () => {
    const n = node({ id: 'n-h' });
    useCanvasStore.setState({ nodes: [n], selectedIds: new Set<string>() });
    const { container } = render(<StoryboardDetail nodeId="n-h" onClose={vi.fn()} />);

    const nodeBox = screen.getByTestId('real-node').parentElement as HTMLElement;
    expect(nodeBox.style.height).toBe('420px');
    expect(nodeBox.style.height).not.toBe('100%');

    // 紧随其后的留白块，撑出容器的可滚高度。
    // （Context.Provider 不产生 DOM 节点，所以 spacer 是 nodeBox 的兄弟。）
    const spacer = nodeBox.nextElementSibling as HTMLElement;
    expect(spacer?.style.height).toBe('460px');
    expect(container).toBeTruthy();
  });

  it('返回箭头与关闭按钮都能收起详情——两个方向的心智各给一个出口', () => {
    useCanvasStore.setState({ nodes: [node({ id: 'n-back' })] });
    const onClose = vi.fn();
    render(<StoryboardDetail nodeId="n-back" onClose={onClose} />);

    fireEvent.click(screen.getByRole('button', { name: '返回' }));
    expect(onClose).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('⋯ 菜单只有「创建副本」和「删除」两项', () => {
    useCanvasStore.setState({ nodes: [node({ id: 'n-menu' })] });
    render(<StoryboardDetail nodeId="n-menu" onClose={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: '更多' }));
    expect(screen.getByRole('button', { name: /创建副本/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /删除/ })).toBeInTheDocument();
    // 「添加到 AI 对话」归计划二（AI 对话重构），现在不该出现
    expect(screen.queryByRole('button', { name: /对话/ })).toBeNull();
  });

  it('创建副本：store 里多出一个同内容的节点', () => {
    useCanvasStore.setState({ nodes: [node({ id: 'n-dup', content: 'https://x/a.png' })] });
    render(<StoryboardDetail nodeId="n-dup" onClose={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: '更多' }));
    fireEvent.click(screen.getByRole('button', { name: /创建副本/ }));

    const nodes = useCanvasStore.getState().nodes;
    expect(nodes).toHaveLength(2);
    expect(nodes.filter((n) => n.content === 'https://x/a.png')).toHaveLength(2);
  });

  // 删完必须收起面板：节点没了，面板会指向一个不存在的 id。
  it('删除：节点从 store 消失，并收起详情面板', () => {
    useCanvasStore.setState({ nodes: [node({ id: 'n-del' })] });
    const onClose = vi.fn();
    render(<StoryboardDetail nodeId="n-del" onClose={onClose} />);

    fireEvent.click(screen.getByRole('button', { name: '更多' }));
    fireEvent.click(screen.getByRole('button', { name: /删除/ }));

    expect(useCanvasStore.getState().nodes.some((n) => n.id === 'n-del')).toBe(false);
    expect(onClose).toHaveBeenCalled();
  });

  it('节点已不存在时渲染 null', () => {
    const { container } = render(<StoryboardDetail nodeId="gone" onClose={vi.fn()} />);
    expect(container.firstChild).toBeNull();
  });

  it('未知节点类型不崩，渲染 null', () => {
    useCanvasStore.setState({ nodes: [node({ id: 'n-x', type: 'some-future-node' as CanvasNode['type'] })] });
    const { container } = render(<StoryboardDetail nodeId="n-x" onClose={vi.fn()} />);
    expect(container.firstChild).toBeNull();
  });
});
