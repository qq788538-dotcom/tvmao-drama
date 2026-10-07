// @vitest-environment jsdom
import '../testing/domSetup';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App as AntApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const mocks = vi.hoisted(() => ({ models: [] as any[], patch: vi.fn() }));

vi.mock('../api', async (orig) => ({
  ...(await orig<typeof import('../api')>()),
  getGeneral: async () => ({
    version: '0.1.0', dataDir: '/data', baseDomain: 'http://gw:3000', baseDomainModified: false, apiKeySet: true, apiKeyMasked: 'sk-••••••••WXYZ',
  }),
  listModels: async () => mocks.models,
  patchModel: (id: number, p: unknown) => mocks.patch(id, p),
}));

import { ModelCenterPage } from './ModelCenterPage';

const model = (over: Record<string, unknown> = {}) => ({
  id: 1, type: 'image', name: 'GPT Image 2', modelId: 'gpt-image-2', enabled: true, hidden: false, sortOrder: 1,
  basePath: '/v1/images', imageInputFormat: 'url', imageRequestMode: 'json', requestTemplate: {}, pollingTemplate: null,
  uiSchema: null, genControl: null, modifiedFields: [], references: [],
  upstream: { configured: false, baseUrl: '', apiKeyMasked: '' },
  ...over,
});
const configured = { configured: true, baseUrl: 'https://u.example.com', apiKeyMasked: '••••abcd' };

function renderPage() {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AntApp>
        <ModelCenterPage />
      </AntApp>
    </QueryClientProvider>,
  );
}
async function openEditor() {
  fireEvent.click(await screen.findByRole('button', { name: /编\s*辑/ }));
  await screen.findByText('独立上游', { selector: 'h5' });
}

describe('ModelCenterPage 独立上游', () => {
  beforeEach(() => {
    mocks.patch.mockReset();
    mocks.patch.mockImplementation(async (_id: number, _p: unknown) => model());
    mocks.models = [model()];
  });

  it('全局上游卡片出现在模型中心顶部', async () => {
    renderPage();
    expect(await screen.findByText(/上游服务（全局）/)).toBeTruthy();
    await waitFor(() => expect(screen.getByText(/sk-••••••••WXYZ/)).toBeTruthy());
  });

  it('配了独立上游的模型列表里有“独立上游”标签', async () => {
    mocks.models = [model({ upstream: configured })];
    renderPage();
    expect(await screen.findByText('独立上游', { selector: '.ant-tag' })).toBeTruthy();
  });

  it('没配的模型没有标签', async () => {
    renderPage();
    await screen.findByText('GPT Image 2');
    expect(screen.queryByText('独立上游', { selector: '.ant-tag' })).toBeNull();
  });

  it('单独配置、原本没有 Key 时只填地址：被拦下，不发请求', async () => {
    renderPage();
    await openEditor();
    fireEvent.click(screen.getByText('单独配置'));
    await waitFor(() => expect(screen.getAllByPlaceholderText(/your-gateway/).length).toBe(2));
    fireEvent.change(screen.getAllByPlaceholderText(/your-gateway/)[1], { target: { value: 'https://u.example.com' } });
    const saveBtns = screen.getAllByRole('button', { name: /保\s*存/ });
    fireEvent.click(saveBtns[saveBtns.length - 1]);
    expect(await screen.findByText('单独配置上游时，地址和 API Key 都必须填写')).toBeTruthy();
    expect(mocks.patch).not.toHaveBeenCalled();
  });

  it('已有 Key 时只改地址：提交成功，补丁里没有 apiKey', async () => {
    mocks.models = [model({ upstream: configured })];
    renderPage();
    await openEditor();
    expect(screen.getByText(/••••abcd/)).toBeTruthy();
    const url = await screen.findByDisplayValue('https://u.example.com');
    fireEvent.change(url, { target: { value: 'https://new.example.com' } });
    const saveBtns = screen.getAllByRole('button', { name: /保\s*存/ });
    fireEvent.click(saveBtns[saveBtns.length - 1]);
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
    const [id, p] = mocks.patch.mock.calls[0];
    expect(id).toBe(1);
    expect(p).toEqual({ upstream: { baseUrl: 'https://new.example.com' } });
    expect(p.upstream).not.toHaveProperty('apiKey');
  });

  it('切回“使用全局”发出 upstream: null', async () => {
    mocks.models = [model({ upstream: configured })];
    renderPage();
    await openEditor();
    fireEvent.click(screen.getByText('使用全局'));
    const saveBtns = screen.getAllByRole('button', { name: /保\s*存/ });
    fireEvent.click(saveBtns[saveBtns.length - 1]);
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
    expect(mocks.patch.mock.calls[0][1]).toEqual({ upstream: null });
  });
});
