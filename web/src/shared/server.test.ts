import { beforeEach, describe, expect, it, vi } from 'vitest';

const invoke = vi.fn();
vi.mock('@tauri-apps/api/core', () => ({ invoke: (...args: unknown[]) => invoke(...args) }));

type Handler = (e: { payload: unknown }) => void;
let handlers: Handler[] = [];
const fire = (payload: unknown) => handlers.forEach((h) => h({ payload }));
const unlisten = vi.fn();
vi.mock('@tauri-apps/api/event', () => ({
  listen: (name: string, handler: Handler) => {
    expect(name).toBe('sidecar-state');
    handlers.push(handler);
    return Promise.resolve(unlisten);
  },
}));

import {
  DataDirSwitchError,
  authHeaders,
  buildHttpUrl,
  buildMediaUrl,
  buildWsUrl,
  getDataDir,
  getServerInfo,
  getSidecarStatus,
  switchToDefaultDataDir,
  onSidecarStatus,
  resetServerInfoForTest,
  restartSidecar,
} from './server';

const info = { port: 54321, token: 'a+b/c=' };

beforeEach(() => {
  invoke.mockReset();
  handlers = [];
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
    fire({ status: 'starting' });
    expect(seen).toEqual([{ status: 'starting' }]);
    await getServerInfo();
    expect(invoke).toHaveBeenCalledTimes(2); // 缓存已清，重新调用 server_info
    un();
    expect(unlisten).toHaveBeenCalled();
  });
});

// 端口缓存不能只靠 SidecarGate 挂着才失效：Gate 之外缓存过的调用方也要拿到新端口。
describe('端口缓存全局失效', () => {
  it('没有任何组件订阅时，状态事件也会清掉缓存', async () => {
    invoke.mockResolvedValue(info);
    await getServerInfo();
    await Promise.resolve();
    expect(handlers.length).toBe(1); // 首次取 info 时注册的全局监听
    fire({ status: 'starting' });
    await getServerInfo();
    expect(invoke).toHaveBeenCalledTimes(2);
  });

  it('全局监听只注册一次', async () => {
    invoke.mockResolvedValue(info);
    await getServerInfo();
    fire({ status: 'ready' });
    await getServerInfo();
    await restartSidecar();
    await Promise.resolve();
    expect(handlers.length).toBe(1);
  });
});

describe('数据目录', () => {
  it('getDataDir 调用 get_data_dir', async () => {
    const dd = { current: '/Volumes/外接盘/喵剧', default: '/Users/a/Library/x', custom: true };
    invoke.mockResolvedValue(dd);
    expect(await getDataDir()).toEqual(dd);
    expect(invoke).toHaveBeenCalledWith('get_data_dir');
  });

  it('switchToDefaultDataDir 先 set_data_dir(null) 再 restart_sidecar', async () => {
    invoke.mockResolvedValueOnce({ current: '/d', default: '/d', custom: false });
    invoke.mockResolvedValueOnce(info);
    expect(await switchToDefaultDataDir()).toEqual(info);
    expect(invoke.mock.calls).toEqual([['set_data_dir', { path: null }], ['restart_sidecar']]);
  });

  it('set_data_dir 失败时不重启', async () => {
    invoke.mockRejectedValueOnce('无法删除 location.json');
    const err = await switchToDefaultDataDir().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(DataDirSwitchError);
    expect((err as Error).message).toBe('无法切换到默认数据目录：无法删除 location.json');
    expect(invoke).toHaveBeenCalledTimes(1);
  });
});
