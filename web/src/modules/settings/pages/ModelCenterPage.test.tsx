// @vitest-environment jsdom
import '../testing/domSetup';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App as AntApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const mocks = vi.hoisted(() => ({ models: [] as any[], patch: vi.fn(), testConn: vi.fn(), signupUrl: undefined as string | undefined, open: vi.fn(async (_u: string) => {}) }));

vi.mock('@/shared/openExternal', () => ({ openExternal: (u: string) => mocks.open(u) }));

vi.mock('../api', async (orig) => ({
  ...(await orig<typeof import('../api')>()),
  getGeneral: async () => ({
    version: '0.1.0', dataDir: '/data', baseDomain: 'http://gw:3000', baseDomainModified: false, apiKeySet: true, apiKeyMasked: 'sk-••••••••WXYZ', signupUrl: mocks.signupUrl,
  }),
  listModels: async () => mocks.models,
  patchModel: (id: number, p: unknown) => mocks.patch(id, p),
  testModelConnections: () => mocks.testConn(),
}));

import { SettingsApiError } from '../api';
import { ModelCenterPage } from './ModelCenterPage';

const model = (over: Record<string, unknown> = {}) => ({
  id: 1, type: 'image', name: 'GPT Image 2', modelId: 'gpt-image-2', enabled: true, hidden: false, sortOrder: 1,
  basePath: '/v1/images', imageInputFormat: 'url', imageRequestMode: 'json', requestTemplate: {}, pollingTemplate: null,
  uiSchema: null, genControl: null, modifiedFields: [], references: [],
  upstream: { configured: false, baseUrl: '', apiKeyMasked: '' },
  ...over,
});
const CONFIRM = '切回全局后，将清除该模型已保存的独立地址和 API Key，确定吗？';
const configured = { configured: true, baseUrl: 'https://u.example.com', apiKeyMasked: '••••abcd' };

function renderPage(qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return render(
    <QueryClientProvider client={qc}>
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

  it('切回“使用全局”：弹二次确认，确定后发出 upstream: null', async () => {
    mocks.models = [model({ upstream: configured })];
    renderPage();
    await openEditor();
    fireEvent.click(screen.getByText('使用全局'));
    const saveBtns = screen.getAllByRole('button', { name: /保\s*存/ });
    fireEvent.click(saveBtns[saveBtns.length - 1]);
    expect((await screen.findAllByText(CONFIRM)).length).toBeGreaterThan(0);
    expect(mocks.patch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: /确\s*定/ }));
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
    expect(mocks.patch.mock.calls[0][1]).toEqual({ upstream: null });
  });

  it('二次确认点取消：不保存', async () => {
    mocks.models = [model({ upstream: configured })];
    renderPage();
    await openEditor();
    fireEvent.click(screen.getByText('使用全局'));
    const saveBtns = screen.getAllByRole('button', { name: /保\s*存/ });
    fireEvent.click(saveBtns[saveBtns.length - 1]);
    await screen.findAllByText(CONFIRM);
    fireEvent.click(screen.getByRole('button', { name: /取\s*消/ }));
    await waitFor(() => expect(screen.queryByText(/确定吗/)).toBeNull());
    expect(mocks.patch).not.toHaveBeenCalled();
  });

  it('保存成功后 mutation 缓存里没有明文 Key，再次打开 Key 输入框为空', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderPage(qc);
    await openEditor();
    fireEvent.click(screen.getByText('单独配置'));
    await waitFor(() => expect(screen.getAllByPlaceholderText(/your-gateway/).length).toBe(2));
    fireEvent.change(screen.getAllByPlaceholderText(/your-gateway/)[1], { target: { value: 'https://u.example.com' } });
    fireEvent.change(screen.getByPlaceholderText('sk-...'), { target: { value: 'sk-secret-plain' } });
    const saveBtns = screen.getAllByRole('button', { name: /保\s*存/ });
    fireEvent.click(saveBtns[saveBtns.length - 1]);
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
    expect(mocks.patch.mock.calls[0][1]).toEqual({ upstream: { baseUrl: 'https://u.example.com', apiKey: 'sk-secret-plain' } });
    await waitFor(() => expect(JSON.stringify(qc.getMutationCache().getAll().map((m) => m.state))).not.toContain('sk-secret-plain'));
    await waitFor(() => expect(screen.queryByText('独立上游', { selector: 'h5' })).toBeNull());
    // 重新打开：Key 输入框为空
    mocks.models = [model()];
    fireEvent.click(await screen.findByRole('button', { name: /编\s*辑/ }));
    await screen.findByText('独立上游', { selector: 'h5' });
    fireEvent.click(screen.getByText('单独配置'));
    const pw = await screen.findByPlaceholderText('sk-...');
    expect((pw as HTMLInputElement).value).toBe('');
  });

  it('点 X 关闭编辑框：已输入的 Key 被清空', async () => {
    renderPage();
    await openEditor();
    fireEvent.click(screen.getByText('单独配置'));
    const pw = (await screen.findByPlaceholderText('sk-...')) as HTMLInputElement;
    fireEvent.change(pw, { target: { value: 'sk-typed' } });
    expect(pw.value).toBe('sk-typed');
    fireEvent.click(screen.getByRole('button', { name: /close/i }));
    await waitFor(() => expect(screen.queryByText('独立上游', { selector: 'h5' })).toBeNull());
    fireEvent.click(await screen.findByRole('button', { name: /编\s*辑/ }));
    await screen.findByText('独立上游', { selector: 'h5' });
    fireEvent.click(screen.getByText('单独配置'));
    expect(((await screen.findByPlaceholderText('sk-...')) as HTMLInputElement).value).toBe('');
  });

  it('全局卡片给出申请账号链接，点击用系统浏览器打开', async () => {
    mocks.signupUrl = 'https://signup.example.com';
    try {
      renderPage();
      fireEvent.click(await screen.findByText('https://signup.example.com'));
      expect(mocks.open).toHaveBeenCalledWith('https://signup.example.com');
    } finally {
      mocks.signupUrl = undefined;
    }
  });

  it('地址输入框有 newapi 根地址提示（全局卡片 + 独立上游）', async () => {
    renderPage();
    const hint = '填写兼容 newapi 协议的网关根地址，如 https://example.com（无需带 /v1）';
    expect(await screen.findByText(hint)).toBeTruthy();
    await openEditor();
    fireEvent.click(screen.getByText('单独配置'));
    await waitFor(() => expect(screen.getAllByText(hint).length).toBe(2));
  });

  it('Seedance 选单独配置时显示素材提示，其他模型不显示', async () => {
    const tip = /Seedance 已校验素材注册在全局上游账号下/;
    mocks.models = [model({ modelId: 'doubao-seedance-2-0' })];
    const r = renderPage();
    await openEditor();
    expect(screen.queryByText(tip)).toBeNull();
    fireEvent.click(screen.getByText('单独配置'));
    expect(await screen.findByText(tip)).toBeTruthy();
    r.unmount();
    mocks.models = [model()];
    renderPage();
    await openEditor();
    fireEvent.click(screen.getByText('单独配置'));
    await screen.findByPlaceholderText('sk-...');
    expect(screen.queryByText(tip)).toBeNull();
  });

  it('后端 400 的 Key 格式错误显示在 Key 框下，地址错误显示在地址框下', async () => {
    mocks.models = [model({ upstream: configured })];
    mocks.patch.mockRejectedValueOnce(new SettingsApiError(400, 'KEY_FORMAT_BAD', 'upstream.apiKey'));
    renderPage();
    await openEditor();
    fireEvent.change(screen.getByPlaceholderText('留空则保留已保存的 Key'), { target: { value: 'sk-abc' } });
    fireEvent.click(screen.getAllByRole('button', { name: /保\s*存/ }).at(-1)!);
    const el = await screen.findByText('KEY_FORMAT_BAD', { selector: '.ant-form-item-explain-error' });
    expect(el.closest('.ant-form-item')?.querySelector('input[type=password]')).toBeTruthy();
  });
});

describe('ModelCenterPage 编辑框换模型', () => {
  beforeEach(() => {
    mocks.patch.mockReset();
  });

  it('保存 A 后再编辑 B：A 的参数默认值不会串到 B 的表单和补丁里', async () => {
    const opts = [{ value: '16:9' }, { value: '1:1' }];
    const a = model({ id: 1, name: 'Model A', uiSchema: { ratio: { label: '比例', default: '16:9', options: opts } } });
    const b = model({ id: 2, name: 'Model B', modelId: 'model-b', uiSchema: { ratio: { label: '比例', options: opts } } });
    mocks.models = [a, b];
    mocks.patch.mockImplementation(async (id: number, p: { name?: string }) => ({ ...(id === 1 ? a : b), ...p }));
    renderPage();
    await screen.findByText('Model B');
    // 编辑 A：只改名，保存成功后编辑框自动关闭（不走 closeEditor）
    fireEvent.click(screen.getAllByRole('button', { name: /编\s*辑/ })[0]);
    await screen.findByText('独立上游', { selector: 'h5' });
    fireEvent.change(screen.getByDisplayValue('Model A'), { target: { value: 'Model A2' } });
    fireEvent.click(screen.getAllByRole('button', { name: /保\s*存/ }).at(-1)!);
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
    expect(mocks.patch.mock.calls[0]).toEqual([1, { name: 'Model A2' }]);
    await waitFor(() => expect(screen.queryByText('独立上游', { selector: 'h5' })).toBeNull());
    // 编辑 B：B 的 ratio 没有默认值，不改任何东西直接保存 → 不应发补丁
    fireEvent.click(screen.getAllByRole('button', { name: /编\s*辑/ })[1]);
    await screen.findByText('编辑：Model B');
    expect(screen.queryByTitle('16:9')).toBeNull();
    fireEvent.click(screen.getAllByRole('button', { name: /保\s*存/ }).at(-1)!);
    await waitFor(() => expect(screen.queryByText('独立上游', { selector: 'h5' })).toBeNull());
    expect(mocks.patch).toHaveBeenCalledTimes(1);
  }, 20_000);
});

describe('ModelCenterPage 测试连接', () => {
  beforeEach(() => {
    mocks.testConn.mockReset();
    mocks.models = [model(), model({ id: 2, type: 'video', name: 'Seedance', modelId: 'doubao-seedance' })];
  });

  it('点击测试连接只调用一次接口，成功行显示成功、失败行显示失败并可悬停看原因', async () => {
    mocks.testConn.mockResolvedValue({
      results: [
        { id: 1, modelId: 'gpt-image-2', ok: true, message: 'ok' },
        { id: 2, modelId: 'doubao-seedance', ok: false, message: '401 鉴权失败' },
      ],
    });
    renderPage();
    await screen.findByText('GPT Image 2');
    expect(screen.getAllByText('—').length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole('button', { name: /测试连接/ }));
    expect(await screen.findByText('成功')).toBeTruthy();
    expect(mocks.testConn).toHaveBeenCalledTimes(1);
    // 切到视频页：失败行，悬停出现原因；图片页的结果切回后仍在
    fireEvent.click(screen.getByText('视频'));
    const fail = await screen.findByText('失败');
    fireEvent.mouseEnter(fail);
    expect(await screen.findByText('401 鉴权失败')).toBeTruthy();
    fireEvent.click(screen.getByText('图片'));
    expect(await screen.findByText('成功')).toBeTruthy();
  });

  it('请求失败时提示错误信息', async () => {
    mocks.testConn.mockRejectedValue(new SettingsApiError(500, '测试服务挂了'));
    renderPage();
    await screen.findByText('GPT Image 2');
    fireEvent.click(screen.getByRole('button', { name: /测试连接/ }));
    expect(await screen.findByText('测试服务挂了')).toBeTruthy();
  });
});
