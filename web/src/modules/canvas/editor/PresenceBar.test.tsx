import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { PresenceBar } from './PresenceBar';
import { resetCanvasPresence, useCanvasPresenceStore } from './presenceStore';

const setMembers = (members: Array<{ actorUserId: number; email: string }>) =>
  act(() => useCanvasPresenceStore.getState().setMembers(members));

describe('在线成员头像条', () => {
  beforeEach(() => resetCanvasPresence());
  afterEach(() => act(() => resetCanvasPresence()));

  it('按名册渲染头像：首字母 + 稳定色，自己那个标「我」', () => {
    setMembers([
      { actorUserId: 3, email: 'alice@x.com' },
      { actorUserId: 9, email: 'bob@x.com' },
    ]);
    const { container } = render(<PresenceBar selfEmail="bob@x.com" />);

    expect(container.querySelectorAll('[data-presence-member]')).toHaveLength(2);
    expect(screen.getByText('A')).toBeInTheDocument();
    const self = container.querySelector('[data-presence-member="9"]') as HTMLElement;
    expect(self.textContent).toBe('我');
    // 同一个人在每个人屏幕上颜色一致（邮箱算的色相），所以两个人颜色不同。
    const other = container.querySelector('[data-presence-member="3"]') as HTMLElement;
    expect(other.style.backgroundColor).not.toBe(self.style.backgroundColor);
  });

  it('邮箱大小写不同也认得出自己', () => {
    setMembers([
      { actorUserId: 3, email: 'alice@x.com' },
      { actorUserId: 9, email: 'Bob@X.com' },
    ]);
    const { container } = render(<PresenceBar selfEmail="bob@x.com" />);
    expect((container.querySelector('[data-presence-member="9"]') as HTMLElement).textContent).toBe('我');
  });

  it('邮箱缺失的成员用 ? 占位，仍然入册（人在就该看得见）', () => {
    setMembers([
      { actorUserId: 3, email: '' },
      { actorUserId: 9, email: 'bob@x.com' },
    ]);
    const { container } = render(<PresenceBar selfEmail="bob@x.com" />);
    expect((container.querySelector('[data-presence-member="3"]') as HTMLElement).textContent).toBe('?');
  });

  // 只有自己在线时这条头像条纯粹是噪音，还占着顶栏下方的横向空间。
  it('名册空或只有自己时不渲染', () => {
    const { container, rerender } = render(<PresenceBar selfEmail="bob@x.com" />);
    expect(container.querySelector('[data-presence-bar]')).toBeNull();

    setMembers([{ actorUserId: 9, email: 'bob@x.com' }]);
    rerender(<PresenceBar selfEmail="bob@x.com" />);
    expect(container.querySelector('[data-presence-bar]')).toBeNull();
  });

  it('名册更新即时反映（成员离开就少一个头像）', () => {
    setMembers([
      { actorUserId: 3, email: 'alice@x.com' },
      { actorUserId: 9, email: 'bob@x.com' },
    ]);
    const { container } = render(<PresenceBar selfEmail="bob@x.com" />);
    expect(container.querySelectorAll('[data-presence-member]')).toHaveLength(2);

    setMembers([
      { actorUserId: 3, email: 'alice@x.com' },
      { actorUserId: 9, email: 'bob@x.com' },
      { actorUserId: 11, email: 'carol@x.com' },
    ]);
    expect(container.querySelectorAll('[data-presence-member]')).toHaveLength(3);
  });
});
