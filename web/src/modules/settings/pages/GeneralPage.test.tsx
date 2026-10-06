// @vitest-environment jsdom
import '../testing/domSetup';
import { describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { App as AntApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.mock('../api', async (orig) => ({
  ...(await orig<typeof import('../api')>()),
  getGeneral: async () => ({
    version: '0.1.0', dataDir: '/data', baseDomain: 'http://gw:3000', baseDomainModified: true, apiKeySet: true, apiKeyMasked: 'sk-••••••••WXYZ',
  }),
  dataDirStatus: async () => ({ current: '/data', defaultDir: '/default', lastError: '数据目录迁移失败，仍在使用原目录：磁盘已满' }),
  appVersion: async () => '0.1.0',
}));
vi.mock('@tauri-apps/plugin-dialog', () => ({ open: vi.fn() }));

import { buildUpstreamUpdate, dataDirChangeDialog, GeneralPage, pickDataDir } from './GeneralPage';

const initial = { version: '1', dataDir: '/d', baseDomain: 'http://a', baseDomainModified: false, apiKeySet: true, apiKeyMasked: 'sk-••••••••abcd' };

describe('buildUpstreamUpdate', () => {
  it('只提交变化的字段；Key 输入框留空表示不修改', () => {
    expect(buildUpstreamUpdate({ baseDomain: 'http://a', apiKey: '' }, initial)).toBeNull();
    expect(buildUpstreamUpdate({ baseDomain: ' http://b ', apiKey: '' }, initial)).toEqual({ baseDomain: 'http://b' });
    expect(buildUpstreamUpdate({ baseDomain: 'http://a', apiKey: ' sk-new ' }, initial)).toEqual({ apiKey: 'sk-new' });
  });
});

describe('pickDataDir', () => {
  it('取消选择返回 null，选中后交给 sidecar 校验', async () => {
    expect(await pickDataDir('/cur', async () => null, async () => ({ path: 'x', state: 'empty' as const }))).toBeNull();
    const check = vi.fn(async (p: string) => ({ path: p + '/norm', state: 'empty' as const }));
    expect(await pickDataDir('/cur', async () => '/new', check)).toEqual({ path: '/new/norm', state: 'empty' });
    expect(check).toHaveBeenCalledWith('/new');
  });
  it('已有喵剧数据的目录原样带回 state=existing', async () => {
    const check = async () => ({ path: '/Volumes/X/MiaojuData', state: 'existing' as const });
    expect(await pickDataDir('/cur', async () => '/Volumes/X', check)).toEqual({ path: '/Volumes/X/MiaojuData', state: 'existing' });
  });
  it('校验失败时抛出服务端文案', async () => {
    await expect(pickDataDir('/cur', async () => '/x', async () => { throw new Error('所选文件夹不是空的'); })).rejects.toThrow('不是空的');
  });
});

describe('GeneralPage', () => {
  it('只显示 Key 掩码，显示版本、数据目录和上次迁移错误', async () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <AntApp>
          <GeneralPage />
        </AntApp>
      </QueryClientProvider>,
    );
    await waitFor(() => expect(screen.getByText(/sk-••••••••WXYZ/)).toBeTruthy());
    expect(screen.getAllByText('0.1.0').length).toBeGreaterThan(0);
    expect(screen.getByText('/data')).toBeTruthy();
    expect(screen.getByText(/磁盘已满/)).toBeTruthy();
    expect(screen.getByText('已修改')).toBeTruthy();
  });
});

// I1：按校验状态给出不同的按钮和文案
describe('dataDirChangeDialog', () => {
  it('空目录：迁移', () => {
    const d = dataDirChangeDialog({ path: '/X/MiaojuData', state: 'empty' });
    expect(d.mode).toBe('migrate');
    expect(d.okText).toBe('立即重启并迁移');
  });
  it('已有喵剧数据：直接使用该目录（不迁移）', () => {
    const d = dataDirChangeDialog({ path: '/X/MiaojuData', state: 'existing' });
    expect(d.mode).toBe('use-existing');
    expect(d.okText).toBe('直接使用该目录（不迁移）');
    expect(d.description).toContain('不会复制');
  });
});
