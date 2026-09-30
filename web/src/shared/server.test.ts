import { beforeEach, describe, expect, it, vi } from 'vitest';

const invoke = vi.fn();
vi.mock('@tauri-apps/api/core', () => ({ invoke: (...args: unknown[]) => invoke(...args) }));

import {
  authHeaders,
  buildHttpUrl,
  buildMediaUrl,
  buildWsUrl,
  getServerInfo,
  resetServerInfoForTest,
} from './server';

const info = { port: 54321, token: 'a+b/c=' };

beforeEach(() => {
  invoke.mockReset();
  resetServerInfoForTest();
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

  // sidecar 没起来时 server_info 会 reject；失败不能被缓存，否则用户点“重试”永远失败。
  it('失败时抛出原因且不缓存，下次调用会重试', async () => {
    invoke.mockRejectedValueOnce('本地服务启动失败：找不到 sidecar 程序');
    await expect(getServerInfo()).rejects.toBe('本地服务启动失败：找不到 sidecar 程序');
    invoke.mockResolvedValueOnce(info);
    expect(await getServerInfo()).toEqual(info);
    expect(invoke).toHaveBeenCalledTimes(2);
  });
});
