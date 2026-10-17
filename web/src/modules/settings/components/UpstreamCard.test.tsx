// @vitest-environment jsdom
import '../testing/domSetup';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App as AntApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const general = { version: '1', dataDir: '/d', baseDomain: 'http://a', baseDomainModified: false, apiKeySet: true, apiKeyMasked: 'sk-••••••••abcd' };
const mocks = vi.hoisted(() => ({ update: vi.fn() }));
vi.mock('../api', async (orig) => ({
  ...(await orig<typeof import('../api')>()),
  getGeneral: async () => general,
  updateUpstream: (u: unknown) => mocks.update(u),
}));

import { buildUpstreamUpdate, UpstreamCard } from './UpstreamCard';

describe('buildUpstreamUpdate', () => {
  it('只提交变化的字段；Key 输入框留空表示不修改', () => {
    expect(buildUpstreamUpdate({ baseDomain: 'http://a', apiKey: '' }, general)).toBeNull();
    expect(buildUpstreamUpdate({ baseDomain: ' http://b ', apiKey: '' }, general)).toEqual({ baseDomain: 'http://b' });
    expect(buildUpstreamUpdate({ baseDomain: 'http://a', apiKey: ' sk-new ' }, general)).toEqual({ apiKey: 'sk-new' });
  });
});

describe('UpstreamCard Key 不残留', () => {
  it('保存成功后 mutation 缓存里搜不到明文 Key，输入框已清空', async () => {
    mocks.update.mockResolvedValue(general);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <AntApp>
          <UpstreamCard />
        </AntApp>
      </QueryClientProvider>,
    );
    await screen.findByDisplayValue('http://a');
    const input = document.querySelector('input[type=password]') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'sk-secret-plain' } });
    fireEvent.click(screen.getByRole('button', { name: /保\s*存/ }));
    await waitFor(() => expect(mocks.update).toHaveBeenCalledWith({ apiKey: 'sk-secret-plain' }));
    await waitFor(() => expect(JSON.stringify(qc.getMutationCache().getAll().map((m) => m.state))).not.toContain('sk-secret-plain'));
    await waitFor(() => expect(input.value).toBe(''));
  });
});
