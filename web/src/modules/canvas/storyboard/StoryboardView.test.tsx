import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { StoryboardView } from './StoryboardView';
import { useCanvasStore } from '../editor/store';
import type { CanvasNode } from '../editor/types';

// 详情面板里的节点组件用一个真组件替身，暴露一个会调用 store.deleteNodes
// 的按钮——用来验证「故事板里删节点 -> store 里没了」这条方向（真节点组件
// 的删除入口各不相同，但最终都是落到 store.deleteNodes，替身只模拟这一步，
// 断言仍然落在真 store 上，不是自娱自乐的假绿）。
vi.mock('../editor/registry', () => ({
  nodeComponents: {
    'gen-image': MockRealNode,
    'gen-video': MockRealNode,
    'text-node': MockRealNode,
    'gen-audio': MockRealNode,
  },
}));

function MockRealNode({ id }: { id: string }) {
  return (
    <div data-testid="real-node">
      节点 {id}
      <button type="button" onClick={() => useCanvasStore.getState().deleteNodes([id])}>
        删除节点
      </button>
    </div>
  );
}

// jsdom 下所有元素高度为 0,虚拟化会算出空可视区,卡片一张都不渲染,
// 布局与状态机就没法测。这里把 ZoneList 换成朴素全量渲染,测的是布局与
// 状态机本身——虚拟化的正确性不归这个测试管。
vi.mock('./ZoneList', () => ({
  ZoneList: ({ nodes, focusedId, onSelect }: {
    nodes: CanvasNode[]; focusedId: string | null; onSelect: (id: string) => void;
  }) => (
    <div>
      {nodes.map((n) => (
        <button key={n.id} type="button" aria-pressed={n.id === focusedId} onClick={() => onSelect(n.id)}>
          {(n.settings?.name as string) ?? n.id}
        </button>
      ))}
    </div>
  ),
}));

function node(id: string, type: CanvasNode['type'], name: string): CanvasNode {
  return { id, type, x: 0, y: 0, width: 200, height: 200, settings: { name }, status: 'idle' };
}

const NODES = [
  node('a-1', 'gen-audio', '音频一'),
  node('t-1', 'text-node', '文本一'),
  node('i-1', 'gen-image', '图片一'),
  node('v-1', 'gen-video', '视频一'),
];

describe('StoryboardView 默认态', () => {
  beforeEach(() => {
    useCanvasStore.setState({ nodes: NODES, selectedIds: new Set<string>() });
  });

  it('四个分区都在：音频、文本、图片、视频', () => {
    render(<StoryboardView />);
    expect(screen.getByTestId('zone-audio')).toBeInTheDocument();
    expect(screen.getByTestId('zone-text')).toBeInTheDocument();
    expect(screen.getByTestId('zone-image')).toBeInTheDocument();
    expect(screen.getByTestId('zone-video')).toBeInTheDocument();
  });

  it('默认态不显示详情面板', () => {
    render(<StoryboardView />);
    expect(screen.queryByTestId('storyboard-detail')).toBeNull();
  });

  it('节点按分区落到各自的栏里', () => {
    render(<StoryboardView />);
    expect(within(screen.getByTestId('zone-image')).getByText('图片一')).toBeInTheDocument();
    expect(within(screen.getByTestId('zone-video')).getByText('视频一')).toBeInTheDocument();
  });
});

describe('StoryboardView 详情态', () => {
  beforeEach(() => {
    useCanvasStore.setState({ nodes: NODES, selectedIds: new Set<string>() });
  });

  it('点文本：出详情面板，左栏保持「上音频下文本」', () => {
    render(<StoryboardView />);
    fireEvent.click(screen.getByText('文本一'));

    expect(screen.getByTestId('storyboard-detail')).toBeInTheDocument();
    expect(screen.getByTestId('zone-audio')).toBeInTheDocument();
    expect(screen.getByTestId('zone-text')).toBeInTheDocument();
    // 中右让位给详情，图片/视频栏消失
    expect(screen.queryByTestId('zone-image')).toBeNull();
    expect(screen.queryByTestId('zone-video')).toBeNull();
  });

  it('点图片：左下槽位从文本换成图片列表，音频始终在', () => {
    render(<StoryboardView />);
    fireEvent.click(screen.getByText('图片一'));

    expect(screen.getByTestId('storyboard-detail')).toBeInTheDocument();
    expect(screen.getByTestId('zone-audio')).toBeInTheDocument();
    expect(screen.getByTestId('zone-image')).toBeInTheDocument();
    expect(screen.queryByTestId('zone-text')).toBeNull();
    expect(screen.queryByTestId('zone-video')).toBeNull();
  });

  it('点视频：左下槽位换成视频列表', () => {
    render(<StoryboardView />);
    fireEvent.click(screen.getByText('视频一'));

    expect(screen.getByTestId('zone-video')).toBeInTheDocument();
    expect(screen.queryByTestId('zone-text')).toBeNull();
    expect(screen.queryByTestId('zone-image')).toBeNull();
  });

  it('详情面板渲染的是被点中那个节点', () => {
    render(<StoryboardView />);
    fireEvent.click(screen.getByText('图片一'));
    expect(screen.getByTestId('real-node')).toHaveTextContent('节点 i-1');
  });

  it('关闭详情回到默认态：四栏全回来', () => {
    render(<StoryboardView />);
    fireEvent.click(screen.getByText('图片一'));
    fireEvent.click(screen.getByRole('button', { name: '关闭' }));

    expect(screen.queryByTestId('storyboard-detail')).toBeNull();
    expect(screen.getByTestId('zone-text')).toBeInTheDocument();
    expect(screen.getByTestId('zone-image')).toBeInTheDocument();
    expect(screen.getByTestId('zone-video')).toBeInTheDocument();
  });

  // 双向的关键断言：故事板不持有任何数据副本,节点在 store 里没了,
  // 详情态必须自己收敛回默认态,而不是卡在一个指向幽灵节点的面板上。
  it('被聚焦的节点从 store 里消失后，自动退回默认态', () => {
    render(<StoryboardView />);
    fireEvent.click(screen.getByText('图片一'));
    expect(screen.getByTestId('storyboard-detail')).toBeInTheDocument();

    // store 更新会同步触发 React 重渲染 + 那个收敛用的 useEffect，
    // 不包 act 的话 React 18 会报 act 警告，且断言可能跑在 effect 之前。
    act(() => {
      useCanvasStore.setState({ nodes: NODES.filter((n) => n.id !== 'i-1') });
    });

    expect(screen.queryByTestId('storyboard-detail')).toBeNull();
    expect(screen.getByTestId('zone-image')).toBeInTheDocument();
  });

  it('画布那边新增的节点，故事板立刻出现（同一份 store，无同步逻辑）', () => {
    render(<StoryboardView />);
    act(() => {
      useCanvasStore.setState({ nodes: [...NODES, node('i-2', 'gen-image', '图片二')] });
    });
    expect(within(screen.getByTestId('zone-image')).getByText('图片二')).toBeInTheDocument();
  });

  // 反方向：「故事板删节点 -> store 里没了」。详情面板渲染的是
  // registry 里的真节点组件，删除入口在那个组件上；这里用 mock 组件暴露一个
  // 触发 deleteNodes 的按钮来验证这条链路，但断言必须落在真 store 上，不能
  // 只测 mock 自己是否被点到——否则就是假绿。
  it('故事板里删节点：store.nodes 里那个节点确实没了', () => {
    render(<StoryboardView />);
    fireEvent.click(screen.getByText('图片一'));
    expect(screen.getByTestId('real-node')).toHaveTextContent('节点 i-1');

    fireEvent.click(screen.getByRole('button', { name: '删除节点' }));

    expect(useCanvasStore.getState().nodes.some((n) => n.id === 'i-1')).toBe(false);
  });
});

// 左下槽位的类型切换器：只在详情态出现（默认态三栏已把三个类型都摆出来了，
// 再切会出现两个同类型列表），默认跟随点了哪个模块，也能手动覆盖。
// 音频为空时整块不渲染——空壳白占左栏顶部一截，而"这个项目没有音频"这件事
// 从面板不在本身就看得出来。
describe('StoryboardView 音频分区的空态', () => {
  it('有音频时才渲染音频面板', () => {
    useCanvasStore.setState({ nodes: NODES, selectedIds: new Set<string>() });
    render(<StoryboardView />);
    expect(screen.getByTestId('zone-audio')).toBeInTheDocument();
  });

  it('一个音频都没有时，音频面板整块不出现（不留「暂无音频」空壳）', () => {
    useCanvasStore.setState({
      nodes: NODES.filter((n) => n.type !== 'gen-audio'),
      selectedIds: new Set<string>(),
    });
    render(<StoryboardView />);
    expect(screen.queryByTestId('zone-audio')).toBeNull();
    expect(screen.queryByText('暂无音频')).toBeNull();
    // 左栏下半那个槽位仍在——隐藏的只有音频那块
    expect(screen.getByTestId('zone-text')).toBeInTheDocument();
  });

  it('音频从无到有时面板出现（同一份 store，不需要额外同步）', () => {
    useCanvasStore.setState({
      nodes: NODES.filter((n) => n.type !== 'gen-audio'),
      selectedIds: new Set<string>(),
    });
    render(<StoryboardView />);
    expect(screen.queryByTestId('zone-audio')).toBeNull();

    act(() => {
      useCanvasStore.setState({ nodes: NODES });
    });
    expect(screen.getByTestId('zone-audio')).toBeInTheDocument();
  });
});

describe('StoryboardView 左下槽位切换', () => {
  beforeEach(() => {
    useCanvasStore.setState({ nodes: NODES, selectedIds: new Set<string>() });
  });

  function openSwitcher() {
    fireEvent.click(screen.getByRole('button', { name: '切换类型' }));
  }

  it('默认态不给切换器——三栏已经把三个类型都摆出来了', () => {
    render(<StoryboardView />);
    expect(screen.queryByRole('button', { name: '切换类型' })).toBeNull();
  });

  it('进详情态才出现切换器', () => {
    render(<StoryboardView />);
    fireEvent.click(within(screen.getByTestId('zone-image')).getByText('图片一'));
    expect(screen.getByRole('button', { name: '切换类型' })).toBeInTheDocument();
  });

  it('菜单里三个可切换类型都在，音频不在（它是左栏上半独立固定的一条）', () => {
    render(<StoryboardView />);
    fireEvent.click(within(screen.getByTestId('zone-image')).getByText('图片一'));
    const trigger = screen.getByRole('button', { name: '切换类型' });
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
    openSwitcher();
    expect(trigger).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('button', { name: '文本' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '图片' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '视频' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '音频' })).toBeNull();
  });

  it('详情态下手动切到视频：左下从图片换成视频列表', () => {
    render(<StoryboardView />);
    fireEvent.click(within(screen.getByTestId('zone-image')).getByText('图片一'));
    expect(screen.getByTestId('zone-image')).toBeInTheDocument();

    openSwitcher();
    fireEvent.click(screen.getByRole('button', { name: '视频' }));

    expect(screen.getByTestId('zone-video')).toBeInTheDocument();
    expect(screen.queryByTestId('zone-image')).toBeNull();
  });

  // 关闭详情必须把手动覆盖一起清掉，否则左下停在用户切过去的类型上不回文本。
  // 这是真实报障：切到视频 → 关详情 → 左下还是「视频」。
  it('关闭详情后左下回到默认的文本，不停在手动切过的类型上', () => {
    render(<StoryboardView />);
    fireEvent.click(within(screen.getByTestId('zone-image')).getByText('图片一'));
    openSwitcher();
    fireEvent.click(screen.getByRole('button', { name: '视频' }));
    expect(screen.getByTestId('zone-video')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '关闭' }));

    expect(screen.queryByTestId('storyboard-detail')).toBeNull();
    expect(screen.getByTestId('zone-text')).toBeInTheDocument();
  });

  it('用返回箭头关闭同样会重置左下槽位', () => {
    render(<StoryboardView />);
    fireEvent.click(within(screen.getByTestId('zone-image')).getByText('图片一'));
    openSwitcher();
    fireEvent.click(screen.getByRole('button', { name: '视频' }));

    fireEvent.click(screen.getByRole('button', { name: '返回' }));

    expect(screen.getByTestId('zone-text')).toBeInTheDocument();
  });

  // 手动选过之后再点卡片，必须回到「跟随」——否则用户点了别的类型却发现
  // 左下还停在他上次手动选的类型上，会以为点击没生效。
  it('手动切过之后再点某张卡片，左下重新跟随那张卡片的类型', () => {
    render(<StoryboardView />);
    fireEvent.click(within(screen.getByTestId('zone-image')).getByText('图片一'));
    openSwitcher();
    fireEvent.click(screen.getByRole('button', { name: '视频' }));
    expect(screen.getByTestId('zone-video')).toBeInTheDocument();

    // 左下现在是视频列表，点里面那条视频
    fireEvent.click(within(screen.getByTestId('zone-video')).getByText('视频一'));

    expect(screen.getByTestId('zone-video')).toBeInTheDocument();
    expect(screen.queryByTestId('zone-image')).toBeNull();
  });
});
