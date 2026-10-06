import { afterEach, describe, expect, it } from 'vitest';
import {
  getSidecarSession,
  resetSidecarSessionForTest,
  setSidecarSession,
  sidecarApiBaseUrl,
  sidecarToken,
} from './sidecarSession';

describe('sidecarSession', () => {
  afterEach(() => resetSidecarSessionForTest());

  it('会话未建立时退回参考仓的相对路径、空 token', () => {
    expect(getSidecarSession()).toBeNull();
    expect(sidecarToken()).toBe('');
    expect(sidecarApiBaseUrl()).toBe('/api/v1');
  });

  it('建立会话后给出 127.0.0.1 上的 REST 地址和 token', () => {
    setSidecarSession({ port: 51234, token: 'tok' });
    expect(sidecarToken()).toBe('tok');
    expect(sidecarApiBaseUrl()).toBe('http://127.0.0.1:51234/api/v1');
  });
});

describe('resolveSidecarWsEndpoint', () => {
  afterEach(() => resetSidecarSessionForTest());

  it('现取会话：来源返回新端口和 token 时用新的', async () => {
    const { resolveSidecarWsEndpoint, setSidecarSessionLoader } = await import('./sidecarSession');
    setSidecarSession({ port: 1, token: 'old' });
    setSidecarSessionLoader(async () => ({ port: 2, token: 'new' }));
    expect(await resolveSidecarWsEndpoint('/api/v1/agent/ws')).toEqual({
      url: 'ws://127.0.0.1:2/api/v1/agent/ws',
      token: 'new',
    });
  });

  it('没有会话时抛错（AgentClient 会按退避重试）', async () => {
    const { resolveSidecarWsEndpoint } = await import('./sidecarSession');
    await expect(resolveSidecarWsEndpoint('/ws')).rejects.toThrow();
  });
});
