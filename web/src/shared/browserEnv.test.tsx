import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { SidecarGate } from './SidecarGate';
import { getServerInfo, onSidecarStatus, resetServerInfoForTest } from './server';

// 纯浏览器（没有 __TAURI_INTERNALS__）：不能出现未处理的 rejection，要给友好提示。
const unhandled: unknown[] = [];
const onUnhandled = (e: unknown) => unhandled.push(e);

afterEach(() => {
  resetServerInfoForTest();
  unhandled.length = 0;
  process.off('unhandledRejection', onUnhandled);
});

describe('非 Tauri 环境', () => {
  it('SidecarGate 显示“请在桌面应用中运行”且无未处理 rejection', async () => {
    process.on('unhandledRejection', onUnhandled);
    expect('__TAURI_INTERNALS__' in window).toBe(false);
    render(<SidecarGate>{() => <div>child</div>}</SidecarGate>);
    await waitFor(() => expect(screen.getByText(/请在桌面应用中运行/)).toBeTruthy());
    await new Promise((r) => setTimeout(r, 50));
    expect(unhandled).toEqual([]);
  });

  it('onSidecarStatus 不 reject，返回可调用的 unlisten', async () => {
    const un = await onSidecarStatus(() => undefined);
    expect(() => un()).not.toThrow();
  });

  it('getServerInfo 以带提示的错误 reject', async () => {
    await expect(getServerInfo()).rejects.toThrow(/请在桌面应用中运行/);
  });

  it('失败页和启动页使用主题前景/背景色', async () => {
    vi.resetModules();
    const { container } = render(<SidecarGate>{() => null}</SidecarGate>);
    expect(container.firstElementChild?.className).toMatch(/bg-background/);
    expect(container.firstElementChild?.className).toMatch(/text-foreground/);
    await waitFor(() => expect(screen.getByText(/请在桌面应用中运行/)).toBeTruthy());
    expect(container.firstElementChild?.className).toMatch(/text-foreground/);
  });
});
