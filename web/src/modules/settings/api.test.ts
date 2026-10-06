import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/shared/server', () => ({
  getServerInfo: async () => ({ port: 4321, token: 't'.repeat(64) }),
  buildHttpUrl: (info: { port: number }, path: string) => `http://127.0.0.1:${info.port}${path}`,
  authHeaders: (info: { token: string }) => ({ Authorization: `Bearer ${info.token}` }),
}));
vi.mock('@tauri-apps/api/core', () => ({ invoke: vi.fn() }));
vi.mock('@tauri-apps/api/app', () => ({ getVersion: async () => '0.1.0' }));

import { invoke } from '@tauri-apps/api/core';
import { applyDataDirChange, getGeneral, patchModel, SettingsApiError } from './api';

const fetchMock = vi.fn();
beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

describe('settings api', () => {
  it('带 token 请求 /api/v1/settings 下的路径', async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ version: '1' }), { status: 200 }));
    await getGeneral();
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe('http://127.0.0.1:4321/api/v1/settings/general');
    expect(init.headers.Authorization).toBe(`Bearer ${'t'.repeat(64)}`);
  });
  it('PATCH 发送 JSON 体', async () => {
    fetchMock.mockResolvedValue(new Response('{}', { status: 200 }));
    await patchModel(7, { name: 'x' });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toMatch(/\/models\/7$/);
    expect(init.method).toBe('PATCH');
    expect(init.headers['Content-Type']).toBe('application/json');
    expect(JSON.parse(init.body)).toEqual({ name: 'x' });
  });
  it('错误响应转成 SettingsApiError，带服务端文案和字段', async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ error: '显示名不能为空', field: 'name' }), { status: 400 }));
    await expect(patchModel(1, { name: '' })).rejects.toMatchObject({ status: 400, message: '显示名不能为空', field: 'name' });
  });
  it('非 JSON 错误体给出通用文案', async () => {
    fetchMock.mockResolvedValue(new Response('oops', { status: 502 }));
    const err = await getGeneral().catch((e) => e);
    expect(err).toBeInstanceOf(SettingsApiError);
    expect(err.message).toContain('502');
  });
  it('applyDataDirChange 调用 Tauri 命令', async () => {
    await applyDataDirChange('/Volumes/X/data');
    expect(invoke).toHaveBeenCalledWith('apply_data_dir_change', { path: '/Volumes/X/data' });
  });
});
