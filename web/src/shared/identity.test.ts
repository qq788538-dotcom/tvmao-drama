import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  switchContext: vi.fn(),
  setToken: vi.fn(),
  clearAuthSession: vi.fn(),
}));
vi.mock('./api', () => apiMock);

import { recoverToPersonalIdentity, switchIdentity } from './identity';

const CLIPBOARD_KEY = 'tvmao:canvas-node-clipboard';

describe('switchIdentity', () => {
  let assignMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    apiMock.switchContext.mockReset();
    apiMock.setToken.mockReset();
    localStorage.clear();
    localStorage.setItem(CLIPBOARD_KEY, 'dummy-clipboard-payload');

    // jsdom 的 window.location 不允许直接 `window.location.assign = fn` 赋值
    // （navigation not implemented 报错），用 defineProperty 换一个可 mock 的替身。
    assignMock = vi.fn();
    Object.defineProperty(window, 'location', {
      value: { ...window.location, assign: assignMock },
      writable: true,
      configurable: true,
    });
  });

  it('成功路径：setToken 被调、剪贴板镜像被清、整页跳转回首页', async () => {
    apiMock.switchContext.mockResolvedValue({ token: 'new-token', teamId: 7, teamName: '团队A' });

    await switchIdentity(7);

    expect(apiMock.switchContext).toHaveBeenCalledWith(7);
    expect(apiMock.setToken).toHaveBeenCalledWith('new-token');
    expect(localStorage.getItem(CLIPBOARD_KEY)).toBeNull();
    expect(assignMock).toHaveBeenCalledWith('/');
  });

  it('失败路径：switchContext 抛错时不换 token、不清剪贴板、不跳转', async () => {
    apiMock.switchContext.mockRejectedValue(new Error('network down'));

    await expect(switchIdentity(7)).rejects.toThrow('network down');

    expect(apiMock.setToken).not.toHaveBeenCalled();
    expect(localStorage.getItem(CLIPBOARD_KEY)).toBe('dummy-clipboard-payload');
    expect(assignMock).not.toHaveBeenCalled();
  });
});

// 死身份自愈（2026-08-18 终审 C1）：团队被解散/自己被移出后，用那份已经死掉的
// 团队 token 换回个人 token，而不是把用户踢下线。
describe('recoverToPersonalIdentity', () => {
  let assignMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    apiMock.switchContext.mockReset();
    apiMock.setToken.mockReset();
    apiMock.clearAuthSession.mockReset();
    localStorage.clear();
    localStorage.setItem(CLIPBOARD_KEY, 'dummy-clipboard-payload');
    assignMock = vi.fn();
    Object.defineProperty(window, 'location', {
      value: { ...window.location, pathname: '/team/manage', href: '', assign: assignMock },
      writable: true,
      configurable: true,
    });
  });

  it('成功路径：切回个人（teamId=0）走与 switchIdentity 完全同一套收尾三步', async () => {
    apiMock.switchContext.mockResolvedValue({ token: 'personal-token', teamId: 0 });

    // 返回 true = 换发成功。拦截器的单飞标志靠这个布尔决定要不要放开下一次尝试
    //（成功不放开——页面正在整页重启；失败才放开）。
    await expect(recoverToPersonalIdentity()).resolves.toBe(true);

    expect(apiMock.switchContext).toHaveBeenCalledWith(0);
    expect(apiMock.setToken).toHaveBeenCalledWith('personal-token');
    expect(localStorage.getItem(CLIPBOARD_KEY)).toBeNull();
    expect(assignMock).toHaveBeenCalledWith('/');
    expect(apiMock.clearAuthSession).not.toHaveBeenCalled();
  });

  it('兜底路径：换发也失败时才清会话跳登录（不抛给调用方，它是拦截器里的 fire-and-forget）', async () => {
    apiMock.switchContext.mockRejectedValue(new Error('401'));

    // 返回 false 而不是抛——但成功/失败必须区分得出来，单飞标志要靠它。
    await expect(recoverToPersonalIdentity()).resolves.toBe(false);

    expect(apiMock.setToken).not.toHaveBeenCalled();
    expect(apiMock.clearAuthSession).toHaveBeenCalled();
    expect(window.location.href).toBe('/login');
  });
});
