import { fireEvent, render } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';

const openExternal = vi.fn(async (_url: string) => {});
vi.mock('@/shared/openExternal', () => ({ openExternal: (url: string) => openExternal(url) }));

import { interceptMarkdownLink } from './markdownLinks';

function Host({ html, onRest }: { html: string; onRest?: () => void }) {
  return (
    <div
      data-testid="host"
      onClick={(e) => {
        if (!interceptMarkdownLink(e)) onRest?.();
      }}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}

beforeEach(() => openExternal.mockClear());

// 回归：markdown 里的 <a href> 走默认导航会把整个桌面应用窗口带到外部站点。
it('点链接（含链接内的子元素）拦下默认导航并交给系统浏览器', () => {
  const { getByText } = render(<Host html={'<p><a href="https://example.com/x"><strong>外链</strong></a></p>'} />);
  const ev = new MouseEvent('click', { bubbles: true, cancelable: true });
  getByText('外链').dispatchEvent(ev);
  expect(ev.defaultPrevented).toBe(true);
  expect(openExternal).toHaveBeenCalledWith('https://example.com/x');
});

it('非 http(s) 链接只拦不开；非链接点击不受影响', () => {
  const rest = vi.fn();
  const { getByText } = render(<Host html={'<a href="mailto:a@b.c">信</a><span>正文</span>'} onRest={rest} />);
  const ev = new MouseEvent('click', { bubbles: true, cancelable: true });
  getByText('信').dispatchEvent(ev);
  expect(ev.defaultPrevented).toBe(true);
  expect(openExternal).not.toHaveBeenCalled();
  fireEvent.click(getByText('正文'));
  expect(rest).toHaveBeenCalledTimes(1);
});
