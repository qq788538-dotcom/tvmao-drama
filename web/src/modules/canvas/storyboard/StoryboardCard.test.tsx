import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { StoryboardCard } from './StoryboardCard';
import { isNodeDragActive } from './dragConnect';
import type { CanvasNode } from '../editor/types';

// useThumbUrl 在缩略图换取完成前返回 undefined（见 editor/thumbs.ts），组件
// 那时渲染骨架而不是 <img>。默认模拟「已换到」，让尺寸相关的用例能直接拿到
// img；「换取中」那个态由下面的专门用例单独覆盖。
const thumbUrl = vi.fn((url?: string | null) => url ?? undefined);
vi.mock('../editor/thumbs', () => ({
  useThumbUrl: (url?: string | null) => thumbUrl(url),
}));

function node(over: Partial<CanvasNode> = {}): CanvasNode {
  return {
    id: 'n-1',
    type: 'gen-image',
    x: 0, y: 0, width: 280, height: 280,
    settings: {},
    status: 'idle',
    ...over,
  };
}

describe('StoryboardCard', () => {
  it('优先显示用户改过的名字', () => {
    render(<StoryboardCard node={node({ settings: { name: '主角定妆照' } })} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByText('主角定妆照')).toBeInTheDocument();
  });

  it('没有自定义名字时显示节点类型的默认标签', () => {
    render(<StoryboardCard node={node()} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByText('AI 绘图')).toBeInTheDocument();
  });

  it('有产物时渲染缩略图', () => {
    const { container } = render(
      <StoryboardCard node={node({ content: 'https://example.com/a.png' })} selected={false} onSelect={vi.fn()} />,
    );
    expect(container.querySelector('img')).toBeTruthy();
  });

  it('没有产物时不渲染 img，改显示占位文案', () => {
    const { container } = render(<StoryboardCard node={node()} selected={false} onSelect={vi.fn()} />);
    expect(container.querySelector('img')).toBeNull();
    expect(screen.getByText('待生成')).toBeInTheDocument();
  });

  // 图片/视频卡片可拖到详情面板参考区建连线；文本卡不参与（用户拍板）。
  it('媒体卡片可拖拽，dragstart 写入节点 id', () => {
    const setData = vi.fn();
    render(
      <StoryboardCard node={node({ id: 'n-drag', content: 'https://x/a.png' })} selected={false} onSelect={vi.fn()} />,
    );
    const card = screen.getByRole('button');
    expect(card).toHaveAttribute('draggable', 'true');

    fireEvent.dragStart(card, { dataTransfer: { setData, effectAllowed: '' } });
    expect(setData).toHaveBeenCalledWith('application/x-tvmao-storyboard-node', 'n-drag');
    // dragstart 同时广播全局「正在拖」——参数面板靠它显示灰虚线 + 提示。
    expect(isNodeDragActive()).toBe(true);

    // dragend 必清（无论 drop 成功与否都会在源上触发），漏清会让面板常亮虚线。
    fireEvent.dragEnd(card);
    expect(isNodeDragActive()).toBe(false);
  });

  it('文本卡片不可拖拽', () => {
    render(
      <StoryboardCard node={node({ type: 'text-node', settings: { name: 't' } })} selected={false} onSelect={vi.fn()} />,
    );
    expect(screen.getByRole('button')).not.toHaveAttribute('draggable', 'true');
  });

  // 这三条钉死媒体的显示规格：**最大宽高是上限，不是固定框**。
  // 媒体按原始比例缩放到能放进 287×161，容器随之收缩——16:9 宽高都顶满，
  // 4:3 高度顶满而窄，竖屏更窄。不变形、不裁剪、不留黑边。
  // 历任错误做法：w-full+原图比例（栏宽一宽就撑满整屏）、固定 aspect-video
  // 框+contain（非 16:9 素材缩在中间、两侧一大片留白）。改回去先让这几条红。
  it('媒体按最大宽高约束，不套固定比例的框', () => {
    const { container } = render(
      <StoryboardCard node={node({ content: 'https://example.com/a.png' })} selected={false} onSelect={vi.fn()} />,
    );
    const img = container.querySelector('img');
    expect(img?.className).toContain('max-w-[287px]');
    expect(img?.className).toContain('max-h-[161px]');
  });

  it('媒体尺寸自适应原始比例——写死宽高或 aspect 会让非 16:9 素材变形/留白', () => {
    const { container } = render(
      <StoryboardCard node={node({ content: 'https://example.com/a.png' })} selected={false} onSelect={vi.fn()} />,
    );
    const img = container.querySelector('img');
    expect(img?.className).toContain('w-auto');
    expect(img?.className).toContain('h-auto');
    expect(img?.className).not.toContain('aspect-video');
    expect(img?.className).not.toContain('object-cover');
  });

  it('待生成占位没有比例可循，用固定 287×161', () => {
    render(<StoryboardCard node={node()} selected={false} onSelect={vi.fn()} />);
    const frame = screen.getByText('待生成').parentElement;
    expect(frame?.className).toContain('w-[287px]');
    expect(frame?.className).toContain('h-[161px]');
  });

  it('有 modelId 就显示模型名', () => {
    render(<StoryboardCard node={node({ settings: { modelId: 'seedream-4.0' } })} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByText('seedream-4.0')).toBeInTheDocument();
  });

  it('生成中显示状态', () => {
    render(<StoryboardCard node={node({ status: 'generating' })} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByText('生成中')).toBeInTheDocument();
  });

  it('失败时显示状态', () => {
    render(<StoryboardCard node={node({ status: 'failed' })} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByText('失败')).toBeInTheDocument();
  });

  it('点击回调带上 nodeId', () => {
    const onSelect = vi.fn();
    render(<StoryboardCard node={node({ id: 'n-42' })} selected={false} onSelect={onSelect} />);
    fireEvent.click(screen.getByRole('button'));
    expect(onSelect).toHaveBeenCalledWith('n-42');
  });

  it('选中态标在 aria-pressed 上，方便无障碍与测试', () => {
    render(<StoryboardCard node={node()} selected={true} onSelect={vi.fn()} />);
    expect(screen.getByRole('button')).toHaveAttribute('aria-pressed', 'true');
  });

  // 四个分区里只有图片区的 content 是图片 URL；上面 9 个用例的 fixture 全是
  // gen-image，这正是列表卡片曾经无脑 <img src={content}> 溜过审查的原因——
  // 下面按分区各补至少一个用例，断言渲染出的是"对的东西"而不是破图。

  describe('视频区', () => {
    it('有 content 时渲染 <video>，不渲染 <img>', () => {
      const { container } = render(
        <StoryboardCard
          node={node({ type: 'gen-video', content: 'https://example.com/a.mp4' })}
          selected={false}
          onSelect={vi.fn()}
        />,
      );
      expect(container.querySelector('video')).toBeTruthy();
      expect(container.querySelector('img')).toBeNull();
      // 列表是用来挑的，不是用来播的：不能带 controls，否则点击会落到播放器
      // 控件上而不是选中卡片。
      expect(container.querySelector('video')).not.toHaveAttribute('controls');
    });

    it('没有 content 时显示待生成占位，不渲染 video/img', () => {
      const { container } = render(
        <StoryboardCard node={node({ type: 'gen-video' })} selected={false} onSelect={vi.fn()} />,
      );
      expect(container.querySelector('video')).toBeNull();
      expect(container.querySelector('img')).toBeNull();
      expect(screen.getByText('待生成')).toBeInTheDocument();
    });
  });

  describe('音频区', () => {
    it('不渲染 img，显示图标 + 标题，且不出现"待生成"', () => {
      const { container } = render(
        <StoryboardCard
          node={node({
            type: 'gen-audio',
            content: 'https://example.com/a.mp3',
            settings: { name: '开场配音' },
          })}
          selected={false}
          onSelect={vi.fn()}
        />,
      );
      expect(container.querySelector('img')).toBeNull();
      expect(container.querySelector('audio')).toBeNull();
      expect(screen.queryByText('待生成')).toBeNull();
      expect(screen.getAllByText('开场配音').length).toBeGreaterThan(0);
    });

    it('没有 content 时也不出现"待生成"——音频区不用这个占位', () => {
      render(<StoryboardCard node={node({ type: 'gen-audio' })} selected={false} onSelect={vi.fn()} />);
      expect(screen.queryByText('待生成')).toBeNull();
    });
  });

  // 文本条目是「图标 + 标题」的一行，不显示正文摘要——列表是用来挑的，
  // 挑文本靠标题不靠正文；正文摘要那几十个字既读不出内容也认不出是哪条，
  // 还让每条占掉图片卡片那么高。
  // 这一组钉死「媒体加载期间不塌高」。根因：useThumbUrl 换取完成前返回
  // undefined，无 src 的 <img> 高度是 0，整列条目塌成一条挤在一起；而虚拟
  // 列表按实测高度定位，高度从 0 变 161 会让后面所有条目位移、滚动位置漂。
  describe('媒体加载期间的占位', () => {
    it('缩略图还没换到时渲染同高度骨架，不渲染无 src 的 img', () => {
      thumbUrl.mockReturnValueOnce(undefined);
      const { container } = render(
        <StoryboardCard node={node({ content: 'https://example.com/a.png' })} selected={false} onSelect={vi.fn()} />,
      );
      expect(container.querySelector('img')).toBeNull();

      const skeleton = container.querySelector('.bg-card');
      expect(skeleton?.className).toContain('w-[287px]');
      expect(skeleton?.className).toContain('h-[161px]');
    });

    it('换取前后媒体行高都是 161——高度不变，虚拟列表才不会位移', () => {
      thumbUrl.mockReturnValueOnce(undefined);
      const pending = render(
        <StoryboardCard node={node({ content: 'https://example.com/a.png' })} selected={false} onSelect={vi.fn()} />,
      );
      const pendingRow = pending.container.querySelector('.h-\\[161px\\]');
      expect(pendingRow).toBeTruthy();
      pending.unmount();

      const ready = render(
        <StoryboardCard node={node({ content: 'https://example.com/a.png' })} selected={false} onSelect={vi.fn()} />,
      );
      const readyRow = ready.container.querySelector('.h-\\[161px\\]');
      expect(readyRow).toBeTruthy();
      expect(ready.container.querySelector('img')).toBeTruthy();
    });

    it('视频元素自带固定行高，metadata 没到也不塌', () => {
      const { container } = render(
        <StoryboardCard
          node={node({ type: 'gen-video', content: 'https://example.com/a.mp4' })}
          selected={false}
          onSelect={vi.fn()}
        />,
      );
      const video = container.querySelector('video');
      expect(video?.className).toContain('h-[161px]');
    });
  });

  describe('文本区', () => {
    it('显示用户设置的标题，不显示正文', () => {
      render(
        <StoryboardCard
          node={node({
            type: 'text-node',
            settings: { name: '剧本创作', text: '这是一段不该出现在列表里的正文' },
          })}
          selected={false}
          onSelect={vi.fn()}
        />,
      );
      expect(screen.getByText('剧本创作')).toBeInTheDocument();
      expect(screen.queryByText('这是一段不该出现在列表里的正文')).toBeNull();
    });

    it('没起过名时回落到类型默认标签', () => {
      render(
        <StoryboardCard node={node({ type: 'text-node', settings: {} })} selected={false} onSelect={vi.fn()} />,
      );
      expect(screen.getByText('文本')).toBeInTheDocument();
    });

    it('正文再长也不进列表，不会把条目撑高', () => {
      const longText = '字'.repeat(200);
      render(
        <StoryboardCard
          node={node({ type: 'text-node', settings: { name: '长文本', text: longText } })}
          selected={false}
          onSelect={vi.fn()}
        />,
      );
      expect(screen.queryByText(longText)).toBeNull();
      expect(screen.queryByText(new RegExp('字{20,}'))).toBeNull();
      expect(screen.getByText('长文本')).toBeInTheDocument();
    });

    it('不出现「待生成」——那是图片/视频没产物时的占位', () => {
      render(
        <StoryboardCard node={node({ type: 'text-node', settings: {} })} selected={false} onSelect={vi.fn()} />,
      );
      expect(screen.queryByText('待生成')).toBeNull();
    });

    it('不渲染 img/video', () => {
      const { container } = render(
        <StoryboardCard node={node({ type: 'text-node', settings: { name: 'x' } })} selected={false} onSelect={vi.fn()} />,
      );
      expect(container.querySelector('img')).toBeNull();
      expect(container.querySelector('video')).toBeNull();
    });
  });
});
