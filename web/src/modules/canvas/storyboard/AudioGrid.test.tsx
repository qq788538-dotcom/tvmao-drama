import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AudioGrid } from './AudioGrid';
import type { CanvasNode } from '../editor/types';

function audio(id: string, over: Partial<CanvasNode> = {}): CanvasNode {
  return {
    id,
    type: 'gen-audio',
    x: 0, y: 0, width: 200, height: 200,
    settings: {},
    status: 'idle',
    ...over,
  };
}

describe('AudioGrid', () => {
  it('空态给一句提示，不撑高', () => {
    render(<AudioGrid nodes={[]} focusedId={null} onSelect={vi.fn()} />);
    expect(screen.getByText('暂无音频')).toBeInTheDocument();
  });

  it('优先显示用户改过的名字', () => {
    render(
      <AudioGrid nodes={[audio('a1', { settings: { name: '开场配乐' } })]} focusedId={null} onSelect={vi.fn()} />,
    );
    expect(screen.getByText('开场配乐')).toBeInTheDocument();
  });

  it('有时长就显示 mm:ss', () => {
    render(<AudioGrid nodes={[audio('a1', { settings: { duration: 65 } })]} focusedId={null} onSelect={vi.fn()} />);
    expect(screen.getByText('01:05')).toBeInTheDocument();
  });

  // 拿不到时长时不填 00:00 —— 那会让「时长未知」和「零秒」看起来一样。
  it('没有时长就不显示时长，不伪造 00:00', () => {
    render(<AudioGrid nodes={[audio('a1')]} focusedId={null} onSelect={vi.fn()} />);
    expect(screen.queryByText('00:00')).toBeNull();
  });

  it('音频卡片不渲染 img/video —— 音频没有可看的画面', () => {
    const { container } = render(
      <AudioGrid nodes={[audio('a1', { content: 'https://x/a.mp3' })]} focusedId={null} onSelect={vi.fn()} />,
    );
    expect(container.querySelector('img')).toBeNull();
    expect(container.querySelector('video')).toBeNull();
  });

  it('点击回调带上 nodeId', () => {
    const onSelect = vi.fn();
    render(<AudioGrid nodes={[audio('a-42')]} focusedId={null} onSelect={onSelect} />);
    fireEvent.click(screen.getByRole('button'));
    expect(onSelect).toHaveBeenCalledWith('a-42');
  });

  it('选中态标在 aria-pressed 上', () => {
    render(<AudioGrid nodes={[audio('a1')]} focusedId="a1" onSelect={vi.fn()} />);
    expect(screen.getByRole('button')).toHaveAttribute('aria-pressed', 'true');
  });

  // 高度策略是这一栏存在的前提：自适应内容、三行封顶后内部纵向滚。
  // 写死高度或不封顶都会让它重新吃掉半个左栏（改之前就是跟下面平分的）。
  it('多个音频时换行排布，最多三行后纵向滚动', () => {
    const nodes = Array.from({ length: 12 }, (_, i) => audio(`a${i}`));
    const { container } = render(<AudioGrid nodes={nodes} focusedId={null} onSelect={vi.fn()} />);

    const scroller = container.firstElementChild as HTMLElement;
    expect(scroller.className).toContain('overflow-y-auto');
    // 118*3 + 12*2 = 378
    expect(scroller.style.maxHeight).toBe('378px');
    expect(scroller.firstElementChild?.className).toContain('flex-wrap');
    expect(screen.getAllByRole('button')).toHaveLength(12);
  });

  it('不横向滚动——横向滚会把后面的卡片藏在视口外', () => {
    const { container } = render(<AudioGrid nodes={[audio('a1')]} focusedId={null} onSelect={vi.fn()} />);
    const scroller = container.firstElementChild as HTMLElement;
    expect(scroller.className).not.toContain('overflow-x-auto');
  });
});
