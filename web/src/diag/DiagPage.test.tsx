import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/shared/server', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/shared/server')>()),
  getServerInfo: vi.fn(async () => ({ port: 50001, token: 't' })),
  // SidecarGate 会订阅 Tauri 事件；测试环境没有 Tauri，桩成空订阅。
  onSidecarStatus: vi.fn(async () => () => {}),
}));

// 用普通函数而不是 vi.fn 承载桩行为：让「检查框架抛错」这条用例的 rejection
// 只经过被测代码一条路径。
let connectionChecksImpl: () => Promise<unknown[]> = async () => [];
vi.mock('./checks', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./checks')>()),
  connectionChecks: () => connectionChecksImpl(),
  webCodecsChecks: vi.fn(async () => []),
}));

import { DiagPage } from './DiagPage';

function renderPage() {
  return render(
    <MemoryRouter>
      <DiagPage />
    </MemoryRouter>,
  );
}

describe('DiagPage', () => {
  beforeEach(() => {
    connectionChecksImpl = async () => [];
  });

  it('检查框架本身抛错时显示失败项，而不是静默', async () => {
    connectionChecksImpl = async () => {
      throw new Error('boom');
    };
    renderPage();
    fireEvent.click(await screen.findByText('1. 运行连接与编解码检查'));
    expect(await screen.findByText(/FAIL 运行检查 — Error: boom/)).toBeInTheDocument();
  });

  it('剪贴板写入被拒时提示手动复制', async () => {
    connectionChecksImpl = async () => [{ name: 'x', ok: true, detail: 'ok', ms: 1 }];
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: vi.fn(async () => { throw new Error('NotAllowedError'); }) },
    });
    renderPage();
    fireEvent.click(await screen.findByText('1. 运行连接与编解码检查'));
    await waitFor(() => expect(screen.getByText('复制结果')).not.toBeDisabled());
    fireEvent.click(screen.getByText('复制结果'));
    expect(await screen.findByText(/复制失败/)).toBeInTheDocument();
  });

  it('独立页面显示返回链接和标题；嵌进设置页时不显示（M-2）', async () => {
    const { unmount } = renderPage();
    expect(await screen.findByText('← 返回画布')).toBeInTheDocument();
    expect(screen.getByText('喵剧AI · 环境诊断')).toBeInTheDocument();
    unmount();

    render(
      <MemoryRouter>
        <DiagPage embedded />
      </MemoryRouter>,
    );
    expect(await screen.findByText('1. 运行连接与编解码检查')).toBeInTheDocument();
    expect(screen.queryByText('← 返回画布')).toBeNull();
    expect(screen.queryByText('喵剧AI · 环境诊断')).toBeNull();
  });

  it('使用 antd 按钮，选择文件后显示文件名', async () => {
    renderPage();
    const btn = (await screen.findByText('选择文件')).closest('button')!;
    expect(btn).toHaveClass('ant-btn');
    expect(screen.getByText('1. 运行连接与编解码检查').closest('button')).toHaveClass('ant-btn');
    expect(screen.getByText('复制结果').closest('button')).toHaveClass('ant-btn');
    expect(screen.getByText('未选择文件')).toBeInTheDocument();
    const input = screen.getByTestId('diag-file-input') as HTMLInputElement;
    expect(input).toHaveStyle({ display: 'none' });
    fireEvent.change(input, { target: { files: [new File(['x'], 'demo.mp4', { type: 'video/mp4' })] } });
    expect(await screen.findByText('demo.mp4')).toBeInTheDocument();
    expect(screen.queryByText('未选择文件')).toBeNull();
  });
});
