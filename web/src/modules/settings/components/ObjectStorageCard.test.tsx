// @vitest-environment jsdom
import '../testing/domSetup';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App as AntApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const save = vi.fn();
const probe = vi.fn();
vi.mock('../api', async (orig) => ({
  ...(await orig<typeof import('../api')>()),
  getObjectStorage: async () => ({
    configured: true, provider: 'tos', endpoint: 'tos-cn-beijing.volces.com', region: 'cn-beijing',
    bucket: 'b', prefix: 'miaoju-inputs', accessKey: 'AK', secretKeySet: true, secretKeyMasked: '••••••••WXYZ',
  }),
  saveObjectStorage: (b: unknown) => save(b),
  testObjectStorage: (b: unknown) => probe(b),
  clearObjectStorage: vi.fn(),
}));

import { ObjectStorageCard } from './ObjectStorageCard';

function renderCard() {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AntApp>
        <ObjectStorageCard />
      </AntApp>
    </QueryClientProvider>,
  );
}

describe('ObjectStorageCard', () => {
  beforeEach(() => {
    save.mockReset().mockResolvedValue({ configured: true });
    probe.mockReset();
  });

  it('回显已保存配置，SecretKey 只显示掩码占位', async () => {
    renderCard();
    expect(await screen.findByDisplayValue('tos-cn-beijing.volces.com')).toBeTruthy();
    expect(screen.getByText('已配置')).toBeTruthy();
    const secret = screen.getByLabelText('SecretKey') as HTMLInputElement;
    expect(secret.value).toBe('');
    expect(secret.placeholder).toContain('WXYZ');
  });

  it('不重输 SecretKey 保存时发送空字符串（后端沿用原值）', async () => {
    renderCard();
    await screen.findByDisplayValue('b');
    fireEvent.change(screen.getByLabelText('Bucket'), { target: { value: 'b2' } });
    fireEvent.click(screen.getByRole('button', { name: /保\s*存/ }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][0]).toMatchObject({ provider: 'tos', bucket: 'b2', secretKey: '' });
  });

  it('测试失败时显示失败环节和原因', async () => {
    probe.mockResolvedValue({ ok: false, step: 'download', message: '通过签名地址下载失败：HTTP 403' });
    renderCard();
    await screen.findByDisplayValue('b');
    fireEvent.click(screen.getByRole('button', { name: '测试连接' }));
    expect(await screen.findByText(/HTTP 403/)).toBeTruthy();
  });
});
