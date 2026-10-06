import { describe, expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ShortcutsHint } from './ShortcutsHint';

describe('ShortcutsHint', () => {
  it('默认只显示一行速览，hover 行头键盘图标即弹出完整面板', () => {
    render(<ShortcutsHint platform="mac" />);
    expect(screen.queryByText('适应画布')).toBeNull();
    fireEvent.mouseEnter(screen.getByRole('button', { name: '查看全部快捷键' }));
    expect(screen.getByText('适应画布')).toBeTruthy();
    expect(screen.getByText('移动画布')).toBeTruthy();
    expect(screen.getByText('双指捏合')).toBeTruthy();
  });

  it('指针移出整个容器后面板收起', () => {
    const { container } = render(<ShortcutsHint platform="mac" />);
    fireEvent.mouseEnter(screen.getByRole('button', { name: '查看全部快捷键' }));
    expect(screen.getByText('适应画布')).toBeTruthy();
    fireEvent.mouseLeave(container.firstElementChild!);
    expect(screen.queryByText('适应画布')).toBeNull();
  });

  it('Mac 显示 ⌘/⇧，Windows 显示 Ctrl/Shift', () => {
    const { unmount } = render(<ShortcutsHint platform="mac" />);
    fireEvent.mouseEnter(screen.getByRole('button', { name: '查看全部快捷键' }));
    expect(screen.getAllByText('⌘').length).toBeGreaterThan(0);
    expect(screen.getByText('⇧')).toBeTruthy();
    expect(screen.queryByText('Ctrl')).toBeNull();
    unmount();

    render(<ShortcutsHint platform="win" />);
    fireEvent.mouseEnter(screen.getByRole('button', { name: '查看全部快捷键' }));
    expect(screen.getAllByText('Ctrl').length).toBeGreaterThan(0);
    expect(screen.getByText('Shift')).toBeTruthy();
    expect(screen.queryByText('⌘')).toBeNull();
  });

  it('第二三栏带竖分割线，第一栏没有', () => {
    render(<ShortcutsHint platform="mac" />);
    fireEvent.mouseEnter(screen.getByRole('button', { name: '查看全部快捷键' }));
    const cols = [
      screen.getByText('缩放').parentElement!,
      screen.getByText('移动画布').parentElement!,
      screen.getByText('其他').parentElement!,
    ];
    expect(cols[0].className).not.toContain('sm:border-l');
    expect(cols[1].className).toContain('sm:border-l');
    expect(cols[2].className).toContain('sm:border-l');
    // preflight:false 的项目里 border-style 默认 none，漏掉 border-solid
    // 会重现"宽度颜色都有却画不出线"。
    expect(cols[1].className).toContain('border-solid');
    expect(cols[2].className).toContain('border-solid');
  });

  it('速览条自身不描边，边框只给键盘图标按钮', () => {
    render(<ShortcutsHint platform="mac" />);
    expect(screen.getByTestId('shortcuts-strip').className).not.toMatch(/\bborder\b/);
    expect(screen.getByRole('button', { name: '查看全部快捷键' }).className).toContain(
      'border-solid',
    );
  });

  it('速览文案随平台切换修饰键名，且不再宣称滚轮缩放', () => {
    const { container, unmount } = render(<ShortcutsHint platform="mac" />);
    expect(container.textContent).toContain('⌘+滚轮缩放');
    expect(container.textContent).not.toContain('· 滚轮缩放');
    unmount();
    const { container: winC } = render(<ShortcutsHint platform="win" />);
    expect(winC.textContent).toContain('Ctrl+滚轮缩放');
  });
});
