import { afterEach, describe, expect, it } from 'vitest';
import { browserSession, getServerInfo, getSidecarStatus, getDataDir, resetServerInfoForTest } from './server';

// 浏览器验证模式：scripts/ratchet/browser-mode.mjs 起的 sidecar 通过地址参数把端口和 token 交给前端。
const TOKEN = 'a'.repeat(64);

function open(search: string) {
  window.history.replaceState(null, '', `/canvas/1${search}`);
}

afterEach(() => {
  resetServerInfoForTest();
  window.history.replaceState(null, '', '/');
});

describe('浏览器验证模式', () => {
  it('从地址参数取端口和 token，并从地址栏去掉', async () => {
    open(`?ratchet_port=4321&ratchet_token=${TOKEN}&tab=x`);
    await expect(getServerInfo()).resolves.toEqual({ port: 4321, token: TOKEN });
    expect(window.location.search).toBe('?tab=x');
    expect(window.location.pathname).toBe('/canvas/1');
  });

  it('参数去掉后，刷新或跳转仍沿用 sessionStorage 里的会话', () => {
    open(`?ratchet_port=4321&ratchet_token=${TOKEN}`);
    expect(browserSession()).toEqual({ port: 4321, token: TOKEN });
    open('');
    expect(browserSession()).toEqual({ port: 4321, token: TOKEN });
  });

  it('sidecar 状态视为就绪', async () => {
    open(`?ratchet_port=4321&ratchet_token=${TOKEN}`);
    await expect(getSidecarStatus()).resolves.toEqual({ status: 'ready' });
  });

  it('其余壳命令照旧报“请在桌面应用中运行”', async () => {
    open(`?ratchet_port=4321&ratchet_token=${TOKEN}`);
    await expect(getDataDir()).rejects.toThrow(/请在桌面应用中运行/);
  });

  it('参数不合法时不进入该模式，也不留在地址栏', async () => {
    for (const q of ['?ratchet_port=0&ratchet_token=' + TOKEN, '?ratchet_port=4321&ratchet_token=short', '?ratchet_port=abc']) {
      resetServerInfoForTest();
      open(q);
      await expect(getServerInfo()).rejects.toThrow(/请在桌面应用中运行/);
      expect(window.location.search).toBe('');
    }
  });

  it('没有参数也没有会话时行为不变', async () => {
    open('');
    expect(browserSession()).toBeNull();
    await expect(getServerInfo()).rejects.toThrow(/请在桌面应用中运行/);
  });
});
