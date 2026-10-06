import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { PublishSharePopover } from '../panels/PublishSharePopover';

// 顶栏分享图标点开的浮层——发布双出口的入口。文案是产品拍板的逐字稿，
// 不是占位描述，测试直接钉死这两行文案，防止后续改文案时悄悄漂移。
describe('PublishSharePopover', () => {
  it('展示发布作品/分享链接两项文案，并触发对应回调', () => {
    const onPickSubmit = vi.fn();
    const onPickShare = vi.fn();
    const onOpenChange = vi.fn();

    render(
      <PublishSharePopover
        open
        onOpenChange={onOpenChange}
        onPickSubmit={onPickSubmit}
        onPickShare={onPickShare}
      >
        <button type="button">trigger</button>
      </PublishSharePopover>,
    );

    expect(screen.getByText('发布作品')).toBeInTheDocument();
    expect(
      screen.getByText('发布你的作品和创作过程，让更多创作者看到。'),
    ).toBeInTheDocument();
    expect(screen.getByText('分享链接')).toBeInTheDocument();
    expect(
      screen.getByText('拥有此链接的人可以查看并复制你的画布。'),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByText('发布作品'));
    expect(onPickSubmit).toHaveBeenCalledTimes(1);
    expect(onPickShare).not.toHaveBeenCalled();

    fireEvent.click(screen.getByText('分享链接'));
    expect(onPickShare).toHaveBeenCalledTimes(1);
  });
});
