import { beforeEach, describe, expect, it, vi } from 'vitest';

const invoke = vi.fn();
vi.mock('@tauri-apps/api/core', () => ({ invoke: (...args: unknown[]) => invoke(...args) }));

let eventHandler: ((e: { payload: unknown }) => void) | null = null;
const unlisten = vi.fn();
vi.mock('@tauri-apps/api/event', () => ({
  listen: (name: string, handler: (e: { payload: unknown }) => void) => {
    expect(name).toBe('sidecar-state');
    eventHandler = handler;
    return Promise.resolve(unlisten);
  },
}));

import {
  authHeaders,
  buildHttpUrl,
  buildMediaUrl,
  buildWsUrl,
  getServerInfo,
  getSidecarStatus,
  onSidecarStatus,
  resetServerInfoForTest,
  restartSidecar,
} from './server';

const info = { port: 54321, token: 'a+b/c=' };

beforeEach(() => {
  invoke.mockReset();
  resetServerInfoForTest();
  eventHandler = null;
});

describe('URL 构造', () => {
  it('HTTP 地址指向 127.0.0.1 的 sidecar 端口', () => {
    expect(buildHttpUrl(info, '/api/v1/ping')).toBe('http://127.0.0.1:54321/api/v1/ping');
  });

  it('媒体地址追加经过编码的 token', () => {
    expect(buildMediaUrl(info, '/api/v1/diag/media/sample.mp4')).toBe(
      'http://127.0.0.1:54321/api/v1/diag/media/sample.mp4?token=a%2Bb%2Fc%3D',
    );
  });

  it('路径已有查询参数时用 & 拼接 token', () => {
    expect(buildMediaUrl(info, '/m?w=400')).toBe('http://127.0.0.1:54321/m?w=400&token=a%2Bb%2Fc%3D');
  });

  it('WebSocket 地址用 ws:// 并带 token', () => {
    expect(buildWsUrl(info, '/api/v1/agent/ws')).toBe(
      'ws://127.0.0.1:54321/api/v1/agent/ws?token=a%2Bb%2Fc%3D',
    );
  });

  it('请求头用 Bearer', () => {
    expect(authHeaders(info)).toEqual({ Authorization: 'Bearer a+b/c=' });
  });
});

describe('getServerInfo', () => {
  it('成功后缓存，只调用一次 server_info', async () => {
    invoke.mockResolvedValue(info);
    expect(await getServerInfo()).toEqual(info);
    expect(await getServerInfo()).toEqual(info);
    expect(invoke).toHaveBeenCalledTimes(1);
    expect(invoke).toHaveBeenCalledWith('server_info');
  });

  // sidecar 没起来时 server_info 会 reject；失败结果不能被缓存，这样之后再次调用会重新调用该命令。
  it('失败时抛出原因且不缓存，下次调用会重试', async () => {
    invoke.mockRejectedValueOnce('本地服务启动失败：找不到 sidecar 程序');
    await expect(getServerInfo()).rejects.toBe('本地服务启动失败：找不到 sidecar 程序');
    invoke.mockResolvedValueOnce(info);
    expect(await getServerInfo()).toEqual(info);
    expect(invoke).toHaveBeenCalledTimes(2);
  });
});

describe('sidecar 状态', () => {
  it('getSidecarStatus 调用 sidecar_status', async () => {
    invoke.mockResolvedValue({ status: 'ready' });
    expect(await getSidecarStatus()).toEqual({ status: 'ready' });
    expect(invoke).toHaveBeenCalledWith('sidecar_status');
  });

  it('restartSidecar 调用 restart_sidecar，并把新端口写入缓存', async () => {
    invoke.mockResolvedValueOnce(info);
    await getServerInfo();
    const fresh = { port: 60000, token: 'new' };
    invoke.mockResolvedValueOnce(fresh);
    expect(await restartSidecar()).toEqual(fresh);
    expect(invoke).toHaveBeenLastCalledWith('restart_sidecar');
    expect(await getServerInfo()).toEqual(fresh);
    expect(invoke).toHaveBeenCalledTimes(2);
  });

  it('restartSidecar 失败不缓存', async () => {
    invoke.mockRejectedValueOnce('本地服务启动失败：x');
    await expect(restartSidecar()).rejects.toBe('本地服务启动失败：x');
    invoke.mockResolvedValueOnce(info);
    expect(await getServerInfo()).toEqual(info);
  });

  it('收到状态事件时回调并清掉端口缓存', async () => {
    invoke.mockResolvedValue(info);
    await getServerInfo();
    const seen: unknown[] = [];
    const un = await onSidecarStatus((s) => seen.push(s));
    eventHandler!({ payload: { status: 'starting' } });
    expect(seen).toEqual([{ status: 'starting' }]);
    await getServerInfo();
    expect(invoke).toHaveBeenCalledTimes(2); // 缓存已清，重新调用 server_info
    un();
    expect(unlisten).toHaveBeenCalled();
  });
});
