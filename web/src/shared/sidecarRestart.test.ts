// sidecar 重启（P8 壳自动重启）后，REST 请求要用新的端口和 token：
// 拦截器每次现取 getServerInfo()，而 getServerInfo 的缓存会在壳广播状态事件时被清掉。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { InternalAxiosRequestConfig } from 'axios';

const invoke = vi.fn();
vi.mock('@tauri-apps/api/core', () => ({ invoke: (...args: unknown[]) => invoke(...args) }));
type Handler = (e: { payload: unknown }) => void;
let handlers: Handler[] = [];
const fire = (payload: unknown) => handlers.forEach((h) => h({ payload }));
vi.mock('@tauri-apps/api/event', () => ({
  listen: (_name: string, handler: Handler) => {
    handlers.push(handler);
    return Promise.resolve(() => undefined);
  },
}));

import { api, getProject } from './api';
import { getServerInfo, resetServerInfoForTest } from './server';
import {
  currentSidecarSession,
  resetSidecarSessionForTest,
  setSidecarSession,
  setSidecarSessionLoader,
} from './sidecarSession';

describe('sidecar 重启后 REST 请求换新会话', () => {
  const realAdapter = api.defaults.adapter;
  let seen: InternalAxiosRequestConfig[] = [];

  beforeEach(() => {
    (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__ = {}; // 模拟 Tauri 环境
    invoke.mockReset();
    handlers = [];
    resetServerInfoForTest();
    seen = [];
    api.defaults.adapter = async (config) => {
      seen.push(config);
      return { data: { id: 3 }, status: 200, statusText: 'OK', headers: {}, config };
    };
  });
  afterEach(() => {
    api.defaults.adapter = realAdapter;
    resetSidecarSessionForTest();
  });

  it('壳广播状态事件后，下一个请求重新取 server_info，用新端口和新 token', async () => {
    invoke.mockResolvedValueOnce({ port: 50001, token: 'old' });
    const first = await getServerInfo();
    setSidecarSession(first);
    setSidecarSessionLoader(getServerInfo);

    await getProject(3);
    expect(seen[0].baseURL).toBe('http://127.0.0.1:50001/api/v1');
    expect(seen[0].headers.Authorization).toBe('Bearer old');

    // 壳自动重启 sidecar：广播 starting → ready，端口和 token 都换了。
    invoke.mockResolvedValueOnce({ port: 50002, token: 'new' });
    fire({ status: 'starting' });
    fire({ status: 'ready' });

    await getProject(3);
    expect(seen[1].baseURL).toBe('http://127.0.0.1:50002/api/v1');
    expect(seen[1].headers.Authorization).toBe('Bearer new');
    expect(invoke).toHaveBeenCalledTimes(2);
  });

  it('没有状态事件时走缓存，不会每个请求都调一次 server_info', async () => {
    invoke.mockResolvedValue({ port: 50001, token: 't' });
    setSidecarSessionLoader(getServerInfo);
    await getProject(3);
    await getProject(3);
    expect(invoke).toHaveBeenCalledTimes(1);
  });
});

describe('currentSidecarSession', () => {
  afterEach(() => resetSidecarSessionForTest());

  it('没设来源（单元测试、纯浏览器调试）时返回同步会话', async () => {
    expect(await currentSidecarSession()).toBeNull();
    setSidecarSession({ port: 1, token: 'a' });
    expect(await currentSidecarSession()).toEqual({ port: 1, token: 'a' });
  });

  it('来源失败（sidecar 重启失败）时退回最后一次的会话，让请求按原样失败而不是卡在拦截器', async () => {
    setSidecarSession({ port: 1, token: 'a' });
    setSidecarSessionLoader(() => Promise.reject(new Error('sidecar exited')));
    expect(await currentSidecarSession()).toEqual({ port: 1, token: 'a' });
  });
});
